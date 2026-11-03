// Package media guarda y sirve los archivos de las páginas (SPEC §4.4). Nunca se sirve
// storage/ como estático: cada lectura pasa por repo con el Scope (SPEC §3.3).
package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif" // decodificador para miniaturas
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/oklog/ulid/v2"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decodificador para miniaturas
)

var (
	ErrTooLarge        = errors.New("file_too_large")
	ErrUnsupportedType = errors.New("unsupported_file_type")
	// ErrInUse: una página que quien borra ve todavía usa el archivo (§4.4).
	ErrInUse = errors.New("media_in_use")
)

// allowed es la lista blanca de tipos, detectados por el contenido y no por la extensión.
// SVG no entra: puede llevar scripts (SPEC §4.4). Los documentos de Office son ZIP.
var allowed = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
	"application/pdf": true, "application/zip": true, "text/plain; charset=utf-8": true,
}

const (
	listLimit = 200        // archivos por pedido en el navegador; para más, se busca por nombre
	thumbMax  = 320        // lado mayor de la miniatura, en px
	maxPixels = 40_000_000 // más que esto no se decodifica: evita bombas de descompresión
)

type store interface {
	CreateMedia(ctx context.Context, s repo.Scope, m repo.Media) error
	MediaByID(ctx context.Context, s repo.Scope, id string) (repo.Media, error)
	MediaList(ctx context.Context, s repo.Scope, q string, limit int) ([]repo.MediaItem, error)
	MediaUsedBy(ctx context.Context, s repo.Scope, id string) ([]repo.PageRef, error)
	TrashMedia(ctx context.Context, s repo.Scope, id string) error
	SharedMedia(ctx context.Context, s repo.Scope, shareID, mediaID string) (repo.Media, string, error)
}

type Service struct {
	store    store
	audit    *audit.Recorder
	dir      string // data_dir/storage
	maxBytes int64
}

func NewService(s store, rec *audit.Recorder, dir string, maxBytes int64) *Service {
	return &Service{store: s, audit: rec, dir: dir, maxBytes: maxBytes}
}

// path: storage/ws_<id>/<asset_id>, sin extensión ni nombre original (SPEC §4.4).
func (s *Service) path(wsID, id string, thumb bool) string {
	p := filepath.Join(s.dir, "ws_"+wsID, id)
	if thumb {
		p += ".thumb"
	}
	return p
}

// Upload guarda el archivo y, si es una imagen, su miniatura.
func (s *Service) Upload(ctx context.Context, sc repo.Scope, actorID, filename string, body io.Reader) (repo.Media, error) {
	m := repo.Media{ID: ulid.Make().String(), Filename: cleanName(filename), UploadedBy: actorID}
	dst := s.path(sc.WsID, m.ID, false)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return repo.Media{}, err
	}

	head := make([]byte, 512)
	n, err := io.ReadFull(body, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return repo.Media{}, err
	}
	if m.MimeType, err = sniff(head[:n]); err != nil {
		return repo.Media{}, err
	}

	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return repo.Media{}, err
	}
	sum := sha256.New()
	written, err := io.Copy(io.MultiWriter(f, sum), io.LimitReader(io.MultiReader(bytes.NewReader(head[:n]), body), s.maxBytes+1))
	err = errors.Join(err, f.Close())
	if err == nil && written > s.maxBytes {
		err = ErrTooLarge
	}
	if err != nil {
		os.Remove(dst)
		return repo.Media{}, err
	}
	m.Size, m.SHA256 = written, hex.EncodeToString(sum.Sum(nil))
	m.HasThumbnail = strings.HasPrefix(m.MimeType, "image/") && s.thumbnail(dst, s.path(sc.WsID, m.ID, true)) == nil

	if err := s.store.CreateMedia(ctx, sc, m); err != nil {
		os.Remove(dst)
		os.Remove(s.path(sc.WsID, m.ID, true))
		return repo.Media{}, err
	}
	// El nombre original no va: es contenido. El tipo y el tamaño bastan para identificarla.
	s.audit.Record(ctx, audit.Event{Action: audit.MediaUpload, WsID: sc.WsID, TargetType: "media", TargetID: m.ID,
		Meta: map[string]any{"mime": m.MimeType, "bytes": m.Size}})
	return m, nil
}

// thumbnail genera la miniatura en Go puro (SPEC §4.4). Si la imagen no se puede leer o es
// demasiado grande, simplemente no hay miniatura: el original igual se guarda.
func (s *Service) thumbnail(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil || cfg.Width*cfg.Height > maxPixels {
		return errors.New("not decodable")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	b := img.Bounds()
	scale := min(1, float64(thumbMax)/float64(max(b.Dx(), b.Dy())))
	out := image.NewRGBA(image.Rect(0, 0, max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))))
	draw.CatmullRom.Scale(out, out.Bounds(), img, b, draw.Over, nil)

	w, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	// JPEG para fotos; PNG para lo que puede tener transparencia.
	if format == "jpeg" {
		err = jpeg.Encode(w, out, &jpeg.Options{Quality: 82})
	} else {
		err = png.Encode(w, out)
	}
	return errors.Join(err, w.Close())
}

// Open devuelve la media si el Scope la puede ver (si no, repo.ErrNotFound) y su archivo.
func (s *Service) Open(ctx context.Context, sc repo.Scope, id string, thumb bool) (repo.Media, *os.File, error) {
	m, err := s.store.MediaByID(ctx, sc, id)
	if err != nil {
		return repo.Media{}, nil, err
	}
	if thumb && !m.HasThumbnail {
		thumb = false
	}
	f, err := os.Open(s.path(sc.WsID, m.ID, thumb))
	if thumb && m.MimeType != "image/jpeg" {
		m.MimeType = "image/png" // la miniatura es PNG salvo para fotos JPEG
	}
	return m, f, err
}

// OpenShared es la media de un artículo compartido con el área de sc (§4.19): solo la que usa la
// versión vigente del artículo, leída del almacenamiento del área de origen. La autorización es
// el vínculo (repo.SharedMedia); cualquier otra media del origen es el 404 uniforme.
func (s *Service) OpenShared(ctx context.Context, sc repo.Scope, shareID, id string, thumb bool) (repo.Media, *os.File, error) {
	m, wsID, err := s.store.SharedMedia(ctx, sc, shareID, id)
	if err != nil {
		return repo.Media{}, nil, err
	}
	if thumb && !m.HasThumbnail {
		thumb = false
	}
	f, err := os.Open(s.path(wsID, m.ID, thumb))
	if thumb && m.MimeType != "image/jpeg" {
		m.MimeType = "image/png"
	}
	return m, f, err
}

// List es el navegador de archivos del área (§4.4): lo que el Scope ve, lo más nuevo primero.
func (s *Service) List(ctx context.Context, sc repo.Scope, q string) ([]repo.MediaItem, error) {
	return s.store.MediaList(ctx, sc, strings.TrimSpace(q), listLimit)
}

// Delete manda el archivo a la papelera (§4.4, §4.5). Si una página que quien borra ve lo usa en
// su versión actual, no lo borra y devuelve esas páginas con ErrInUse: primero se saca de ahí. Las
// revisiones viejas que lo mostraban lo verán roto.
func (s *Service) Delete(ctx context.Context, sc repo.Scope, id string) ([]repo.PageRef, error) {
	m, err := s.store.MediaByID(ctx, sc, id)
	if err != nil {
		return nil, err
	}
	pages, err := s.store.MediaUsedBy(ctx, sc, m.ID)
	if err != nil {
		return nil, err
	}
	if len(pages) > 0 {
		return pages, ErrInUse
	}
	if err := s.store.TrashMedia(ctx, sc, m.ID); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.MediaDelete, WsID: sc.WsID, TargetType: "media", TargetID: m.ID,
		Meta: map[string]any{"mime": m.MimeType, "bytes": m.Size}})
	return nil, nil
}

// Remove borra del disco el archivo y su miniatura. Lo llama solo la purga de la papelera
// (SPEC §4.5), con la media que ya borró de la BD; un archivo que ya no está no es un error.
// Copy copia el archivo (y su miniatura, si tiene) de un área a otra con un id nuevo: publicar
// desde el espacio personal copia la media al área (§7), así cada área tiene sus archivos en su
// carpeta y borrar en una no rompe la otra. Si falla, no deja nada a medias.
func (s *Service) Copy(fromWs, toWs string, m repo.Media) (repo.Media, error) {
	cp := m
	cp.ID = ulid.Make().String()
	if err := os.MkdirAll(filepath.Dir(s.path(toWs, cp.ID, false)), 0o700); err != nil {
		return repo.Media{}, err
	}
	for _, thumb := range []bool{false, true} {
		if thumb && !m.HasThumbnail {
			continue
		}
		if err := copyFile(s.path(fromWs, m.ID, thumb), s.path(toWs, cp.ID, thumb)); err != nil {
			return repo.Media{}, errors.Join(err, s.Remove(toWs, cp.ID))
		}
	}
	return cp, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func (s *Service) Remove(wsID, id string) error {
	var errs error
	// El .enc es el de un sellado (F7.6): el mismo id no puede ser de los dos, así que se prueba.
	for _, p := range []string{s.path(wsID, id, false), s.path(wsID, id, true), s.encPath(wsID, id)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = errors.Join(errs, err)
		}
	}
	return errs
}

// sniff es el tipo por el contenido, si está en la lista blanca (SPEC §4.4).
func sniff(head []byte) (string, error) {
	mime := http.DetectContentType(head)
	if !allowed[mime] || (strings.HasPrefix(mime, "text/") && looksLikeMarkup(head)) {
		return "", ErrUnsupportedType
	}
	return mime, nil
}

// ReadUpload lee un archivo para un sellado (F7.6): las mismas reglas de tipo y tamaño que Upload,
// pero en memoria, porque se cifra entero con la DEK antes de tocar el disco. Devuelve el nombre
// ya limpio para mostrar.
func (s *Service) ReadUpload(filename string, body io.Reader) (name, mime string, data []byte, err error) {
	data, err = io.ReadAll(io.LimitReader(body, s.maxBytes+1))
	if err != nil {
		return "", "", nil, err
	}
	if int64(len(data)) > s.maxBytes {
		return "", "", nil, ErrTooLarge
	}
	if mime, err = sniff(data[:min(len(data), 512)]); err != nil {
		return "", "", nil, err
	}
	return cleanName(filename), mime, data, nil
}

// encPath: storage/ws_<área>/<id>.enc, la media de los sellados (SPEC §13.2).
func (s *Service) encPath(wsID, id string) string { return s.path(wsID, id, false) + ".enc" }

// PutEncrypted guarda un archivo ya cifrado de un sellado. Nunca pisa uno que existe.
func (s *Service) PutEncrypted(wsID, id string, data []byte) error {
	dst := s.encPath(wsID, id)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(dst)
		return err
	}
	return f.Close()
}

// ReadEncrypted devuelve el archivo cifrado de un sellado; descifrarlo es cosa de quien tiene la DEK.
func (s *Service) ReadEncrypted(wsID, id string) ([]byte, error) {
	return os.ReadFile(s.encPath(wsID, id))
}

// looksLikeMarkup atrapa SVG y HTML que el sniffing toma por texto plano (sin <?xml ni
// doctype): por el riesgo de XSS no entran ni como texto (SPEC §4.4).
func looksLikeMarkup(head []byte) bool {
	h := bytes.ToLower(head)
	for _, tag := range []string{"<svg", "<html", "<script", "<!doctype", "<?xml"} {
		if bytes.Contains(h, []byte(tag)) {
			return true
		}
	}
	return false
}

// cleanName deja el nombre original apto para mostrar y para Content-Disposition.
func cleanName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' || r == '"' {
			return -1
		}
		return r
	}, filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	if len([]rune(name)) > 200 {
		name = string([]rune(name)[:200])
	}
	if name == "" || name == "." {
		return "archivo"
	}
	return name
}
