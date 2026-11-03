package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/backup"
	"github.com/athomo001/kudawiki/internal/database"
	"github.com/athomo001/kudawiki/internal/repo"
)

// CheckResult es lo que probó CheckBackup.
type CheckResult struct {
	Manifest backup.Manifest
	Events   int // eventos de la auditoría en la cadena verificada
}

// CheckBackup prueba restaurar un respaldo sin tocar la instalación (§21.2.4): lo descifra si
// hace falta (backupKey), lo extrae en una carpeta temporal bajo tmpRoot comprobando cada parte
// contra el manifiesto, abre la base, corre PRAGMA integrity_check y verifica la cadena de la
// auditoría. La carpeta se borra al terminar.
func CheckBackup(ctx context.Context, archive string, backupKey []byte, tmpRoot string) (CheckResult, error) {
	dir, err := os.MkdirTemp(tmpRoot, ".verify-")
	if err != nil {
		return CheckResult{}, err
	}
	defer os.RemoveAll(dir)
	if strings.HasSuffix(archive, backup.EncSuffix) {
		if backupKey == nil {
			return CheckResult{}, errors.New("the backup is encrypted and there is no backup key")
		}
		plain := filepath.Join(dir, "backup.tar.zst")
		if err := backup.DecryptFile(archive, plain, backupKey); err != nil {
			return CheckResult{}, err
		}
		archive = plain
	}
	root := filepath.Join(dir, "restored")
	m, err := backup.Extract(archive, root)
	if err != nil {
		return CheckResult{}, err
	}
	res := CheckResult{Manifest: m}
	db, err := database.Open(filepath.Join(root, "wiki.db"))
	if err != nil {
		return res, err
	}
	defer db.Close()
	var integrity string
	if err := db.Read.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return res, err
	}
	if integrity != "ok" {
		return res, fmt.Errorf("integrity_check: %s", integrity)
	}
	rep, err := audit.Verify(ctx, repo.New(db))
	if err != nil {
		return res, err
	}
	res.Events = rep.Events
	return res, nil
}
