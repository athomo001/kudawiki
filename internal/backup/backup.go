// Package backup hace, verifica y restaura los respaldos (SPEC §13.3, §13.9): una foto
// consistente de la base (VACUUM INTO, sin frenar las escrituras) y los archivos de storage/,
// en un tar.zst con un manifiesto SHA-256 de cada archivo. La MASTER_KEY nunca va adentro: se
// respalda aparte (§6.3).
package backup

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/database"
	"github.com/klauspost/compress/zstd"
)

// FormatVersion cambia si cambia el formato del archivo; Restore no acepta uno que no conoce.
const FormatVersion = 1

const (
	manifestName  = "manifest.json"
	dbName        = "wiki.db"
	storagePrefix = "storage/"
	timeLayout    = "20060102-150405"
)

type Kind string

const (
	Daily      Kind = "daily"       // el automático; lo rota Prune
	Manual     Kind = "manual"      // kudawiki backup
	PreMigrate Kind = "pre-migrate" // antes de migrar, solo la base (§13.9)
)

// Manifest va al final del archivo: lo que tiene, de qué esquema es y el hash de cada parte.
type Manifest struct {
	Format    int       `json:"format"`
	Kind      Kind      `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
	Schema    int       `json:"schema"`
	Files     []File    `json:"files"`
}

type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Name es el nombre del archivo: el tipo y la hora dicen todo lo que Prune necesita.
func Name(kind Kind, at time.Time) string {
	return fmt.Sprintf("kudawiki-%s-%s.tar.zst", kind, at.UTC().Format(timeLayout))
}

// Create escribe un respaldo en dir y devuelve su ruta. storageDir vacío = solo la base. Se
// arma en un temporal dentro de dir y se renombra al final: nunca queda uno a medias con el
// nombre definitivo. db puede ser el pool de lectura: VACUUM INTO no escribe en la base.
func Create(ctx context.Context, db *sql.DB, storageDir, dir string, kind Kind, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(dir, ".tmp-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	schema, err := database.Version(ctx, db)
	if err != nil {
		return "", err
	}
	snapshot := filepath.Join(tmp, dbName)
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, snapshot); err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}

	archive := filepath.Join(tmp, "archive")
	f, err := os.OpenFile(archive, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	m := Manifest{Format: FormatVersion, Kind: kind, CreatedAt: now.UTC(), Schema: schema}
	err = writeArchive(f, &m, snapshot, storageDir)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	final := filepath.Join(dir, Name(kind, now))
	if err := os.Rename(archive, final); err != nil {
		return "", err
	}
	return final, nil
}

func writeArchive(w io.Writer, m *Manifest, snapshot, storageDir string) error {
	zw, err := zstd.NewWriter(w)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(zw)
	add := func(name, src string) error {
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		st, err := in.Stat()
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: st.Size(), ModTime: st.ModTime(), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		h := sha256.New()
		if _, err := io.Copy(io.MultiWriter(tw, h), in); err != nil {
			return err
		}
		m.Files = append(m.Files, File{Path: name, Size: st.Size(), SHA256: hex.EncodeToString(h.Sum(nil))})
		return nil
	}
	if err := add(dbName, snapshot); err != nil {
		return err
	}
	if storageDir != "" {
		err := filepath.WalkDir(storageDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return err // solo archivos comunes: nada de enlaces que apunten afuera
			}
			rel, err := filepath.Rel(storageDir, p)
			if err != nil {
				return err
			}
			return add(storagePrefix+filepath.ToSlash(rel), p)
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: manifestName, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err := tw.Write(body); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return zw.Close()
}

// Verify lee el archivo entero y comprueba cada parte contra el manifiesto: mismos archivos,
// mismo tamaño, mismo SHA-256. No escribe nada.
func Verify(path string) (Manifest, error) {
	return walk(path, nil)
}

// walk recorre el archivo; si extract no es nil, se le pasa cada parte para que la escriba.
func walk(path string, extract func(name string, r io.Reader) error) (Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close()
	zr, err := zstd.NewReader(f)
	if err != nil {
		return Manifest{}, err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	seen := map[string]File{}
	var m Manifest
	var gotManifest bool
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, fmt.Errorf("read archive: %w", err)
		}
		if h.Typeflag != tar.TypeReg {
			return Manifest{}, fmt.Errorf("unexpected entry %q", h.Name)
		}
		if h.Name == manifestName {
			if err := json.NewDecoder(io.LimitReader(tr, 16<<20)).Decode(&m); err != nil {
				return Manifest{}, fmt.Errorf("manifest: %w", err)
			}
			gotManifest = true
			continue
		}
		if !safeName(h.Name) {
			return Manifest{}, fmt.Errorf("unsafe path %q in archive", h.Name)
		}
		hash := sha256.New()
		var r io.Reader = io.TeeReader(tr, hash)
		var n int64
		if extract != nil {
			cr := &counter{r: r}
			if err := extract(h.Name, cr); err != nil {
				return Manifest{}, err
			}
			n = cr.n
		} else if n, err = io.Copy(io.Discard, r); err != nil {
			return Manifest{}, fmt.Errorf("read %s: %w", h.Name, err)
		}
		seen[h.Name] = File{Path: h.Name, Size: n, SHA256: hex.EncodeToString(hash.Sum(nil))}
	}
	if !gotManifest {
		return Manifest{}, errors.New("archive has no manifest")
	}
	if m.Format != FormatVersion {
		return Manifest{}, fmt.Errorf("unknown backup format %d", m.Format)
	}
	if len(seen) != len(m.Files) {
		return Manifest{}, fmt.Errorf("archive has %d files, manifest lists %d", len(seen), len(m.Files))
	}
	for _, want := range m.Files {
		if got, ok := seen[want.Path]; !ok || got != want {
			return Manifest{}, fmt.Errorf("%s does not match the manifest", want.Path)
		}
	}
	return m, nil
}

type counter struct {
	r io.Reader
	n int64
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// safeName acepta solo la base o un archivo dentro de storage/, sin subir de carpeta: un
// archivo armado a mano no puede escribir fuera de los datos (zip slip).
func safeName(name string) bool {
	if name == dbName {
		return true
	}
	rest, ok := strings.CutPrefix(name, storagePrefix)
	return ok && rest != "" && path.Clean(name) == name && !strings.HasPrefix(path.Clean(rest), "..") &&
		!strings.Contains(name, "\\") && !path.IsAbs(name)
}

// Restore verifica el archivo y lo pone en lugar de la base (y de storage/, si el respaldo lo
// trae). Lo que había no se borra: queda en before-restore-<hora>, junto a storage/, y esa
// carpeta se devuelve ("" si no había nada que apartar). Un
// respaldo de un esquema más nuevo que latest no se restaura (§13.9). Se corre con el
// servidor detenido: el comando lo comprueba antes.
func Restore(archivePath, dbPath, storageDir string, latest int) (m Manifest, previous string, err error) {
	m, err = Verify(archivePath)
	if err != nil {
		return Manifest{}, "", err
	}
	if m.Schema > latest {
		return Manifest{}, "", database.ErrSchemaNewer
	}
	root := filepath.Dir(storageDir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Manifest{}, "", err
	}
	stamp := time.Now().UTC().Format(timeLayout)
	staging, err := os.MkdirTemp(root, ".restore-")
	if err != nil {
		return Manifest{}, "", err
	}
	defer os.RemoveAll(staging)

	// Segunda pasada, extrayendo: el archivo ya se verificó entero, pero cada parte se vuelve
	// a comprobar al escribirla.
	if _, err := walk(archivePath, func(name string, r io.Reader) error {
		dst := filepath.Join(staging, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, r)
		return errors.Join(err, out.Close())
	}); err != nil {
		return Manifest{}, "", err
	}

	hasStorage := false
	for _, f := range m.Files {
		hasStorage = hasStorage || strings.HasPrefix(f.Path, storagePrefix)
	}
	aside := func(src, name string) error {
		if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		previous = filepath.Join(root, "before-restore-"+stamp)
		if err := os.MkdirAll(previous, 0o700); err != nil {
			return err
		}
		return os.Rename(src, filepath.Join(previous, name))
	}
	// La base con su WAL: un -wal viejo junto a una base nueva la corrompería.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := aside(dbPath+suffix, dbName+suffix); err != nil {
			return Manifest{}, "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return Manifest{}, "", err
	}
	if err := os.Rename(filepath.Join(staging, dbName), dbPath); err != nil {
		return Manifest{}, "", err
	}
	if hasStorage {
		if err := aside(storageDir, "storage"); err != nil {
			return Manifest{}, "", err
		}
		if err := os.Rename(filepath.Join(staging, "storage"), storageDir); err != nil {
			return Manifest{}, "", err
		}
	}
	return m, previous, nil
}

// InUse dice si otra conexión tiene abierta la base. En modo WAL, pasar a journal_mode=DELETE
// exige ser la única conexión: si otro proceso (el servidor, aunque esté en otro contenedor
// sobre el mismo volumen) la tiene abierta, SQLite no lo permite. Si lo logra, nadie la usa, y
// de paso el WAL queda volcado al archivo, así la copia que Restore deja aparte está completa.
func InUse(dbPath string) (bool, error) {
	if _, err := os.Stat(dbPath); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(0)")
	if err != nil {
		return false, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode = DELETE`).Scan(&mode); err != nil {
		return true, nil // ocupada (SQLITE_BUSY o LOCKED)
	}
	return mode != "delete", nil
}
