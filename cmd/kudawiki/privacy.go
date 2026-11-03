package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/athomo001/kudawiki/internal/areaio"
	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/config"
	"github.com/athomo001/kudawiki/internal/database"
	"github.com/athomo001/kudawiki/internal/media"
	"github.com/athomo001/kudawiki/internal/repo"
)

// privacyReadme va en el ZIP: qué hay y qué no, y por qué (§13.10).
const privacyReadme = `Datos personales de %s en Kudawiki, exportados el %s.

Cada archivo .json es una parte de lo que la wiki guarda de esta persona:
  profile            perfil de la cuenta
  memberships        áreas y rol en cada una
  sessions           sesiones abiertas (IP y navegador)
  login_history      IP y navegadores vistos, para el aviso de inicio de sesión nuevo (1 año)
  notifications      notificaciones (las leídas se borran a los 90 días)
  quiz_attempts      intentos de cuestionarios (retención: quizzes.retention_days)
  read_acknowledgements, read_views   confirmaciones de lectura y aperturas de páginas con acuse
  revisions_authored cuántas revisiones de páginas escribió en cada área
  audit              lo que hizo, según la auditoría
personal-space.zip es su espacio personal, en el mismo formato que la exportación de un área.

No se incluye:
  - el contenido de las páginas de las áreas: es de la organización; el autor queda con su nombre
  - los documentos sellados: están cifrados y nunca salen de la wiki (§6)
  - secretos: contraseña, códigos de 2FA, passkeys, llaves de bóveda y tokens de sesión
  - las copias de seguridad, que conservan estos datos hasta que vencen
`

// runPrivacy es `kudawiki privacy export <email> [--out archivo.zip]`: el derecho de acceso de
// §13.10, desde la consola de quien opera la wiki. Queda en la auditoría.
func runPrivacy(ctx context.Context, cfg config.Config, args []string, out string) error {
	if len(args) != 2 || args[0] != "export" {
		return errors.New("usage: kudawiki privacy export <email> [--out file.zip]")
	}
	// #nosec G703 -- ruta que da quien opera la CLI, no un usuario de la wiki
	if _, err := os.Stat(cfg.DBPath()); err != nil {
		return fmt.Errorf("no database at %s (run `kudawiki init` first)", cfg.DBPath())
	}
	db, err := database.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	if pending, err := db.Pending(ctx); err != nil {
		return err
	} else if len(pending) > 0 {
		return errors.New("the database has pending migrations: start the server once to apply them")
	}
	store := repo.New(db)
	u, err := store.UserByEmail(ctx, args[1])
	if err != nil {
		return fmt.Errorf("there is no account %s", args[1])
	}
	if out == "" {
		out = "kudawiki-personal-" + u.ID + ".zip"
	}
	rec := audit.NewRecorder(store, time.Now, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	// #nosec G703 -- ruta que da quien opera la CLI, no un usuario de la wiki
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // datos personales: solo para quien los pidió
	if err != nil {
		return err
	}
	err = writePersonal(ctx, store, media.NewService(store, rec, cfg.StorageDir(), cfg.Upload.MaxBytes), u, f)
	if err = errors.Join(err, f.Close()); err != nil {
		// #nosec G703 -- ruta que da quien opera la CLI, no un usuario de la wiki
		os.Remove(out)
		return err
	}
	rec.Record(ctx, audit.Event{Action: audit.PrivacyExport, TargetType: "user", TargetID: u.ID, System: "cli"})
	fmt.Println("wrote", out)
	return nil
}

func writePersonal(ctx context.Context, store *repo.Repo, files *media.Service, u repo.User, w io.Writer) error {
	zw := zip.NewWriter(w)
	add := func(name string, b []byte) error {
		dst, err := zw.Create(name)
		if err == nil {
			_, err = dst.Write(b)
		}
		return err
	}
	now := time.Now().UTC()
	if err := add("LEEME.txt", fmt.Appendf(nil, privacyReadme, u.Email, now.Format(time.DateOnly))); err != nil {
		return err
	}
	sections, err := store.PersonalData(ctx, u.ID)
	if err != nil {
		return err
	}
	for _, s := range sections {
		b, _ := json.MarshalIndent(s.Rows, "", "  ")
		if err := add(s.Name+".json", b); err != nil {
			return err
		}
	}
	// El espacio personal es suyo entero: se exporta con el rango del dueño (§7).
	if ws, err := store.PersonalWorkspaceOf(ctx, u.ID); err == nil {
		var buf bytes.Buffer
		if _, err := areaio.Export(ctx, store, files, repo.Scope{WsID: ws.ID, MaxRank: 3}, ws, &buf, now); err != nil {
			return err
		}
		if err := add("personal-space.zip", buf.Bytes()); err != nil {
			return err
		}
	} else if !errors.Is(err, repo.ErrNotFound) {
		return err
	}
	return zw.Close()
}
