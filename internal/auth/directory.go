package auth

import (
	"context"
	"errors"

	"github.com/athomo001/kudawiki/internal/repo"
)

// Login con el directorio de la empresa (SPEC §8.6.2, §8.6.3). auth decide cuándo se consulta
// y abre la sesión; validar a la persona, darle su cuenta y sus áreas lo hace quien implementa
// Directory (internal/ldap), que devuelve estos errores.

var (
	// ErrDirectoryUnavailable: el directorio no respondió. Nunca se dice "contraseña incorrecta".
	ErrDirectoryUnavailable = errors.New("directory_unavailable")
	// ErrPasswordExpired: AD solo lo dice si la contraseña era correcta (532, 773), así que no
	// revela nada de una cuenta ajena.
	ErrPasswordExpired = errors.New("password_expired")
	// ErrNoDirectoryAccess: la persona existe y la contraseña es correcta, pero no está en ningún
	// grupo con regla (§8.6.3).
	ErrNoDirectoryAccess = errors.New("no_directory_access")
	// ErrAccountConflict: ya hay otra cuenta con ese correo; no se fusionan solas (§8.6.3).
	ErrAccountConflict = errors.New("account_conflict")
	// ErrDirectoryManaged: el nombre y el correo de una cuenta del directorio vienen de ahí.
	ErrDirectoryManaged = errors.New("directory_managed")
)

// Directory valida a una persona contra el directorio. Login le da su cuenta (la crea la primera
// vez) y sus áreas; un login que no es del directorio, o una contraseña equivocada, es
// ErrInvalidCredentials. VerifyPassword es la reautenticación: un bind, sin tocar la cuenta.
// PasswordHelpURL es la página de la empresa para la contraseña de la red; vacía si no hay.
type Directory interface {
	Login(ctx context.Context, login, password string) (repo.User, error)
	VerifyPassword(ctx context.Context, u repo.User, password string) error
	PasswordHelpURL(ctx context.Context) string
}

func (s *Service) SetDirectory(d Directory) { s.directory = d }

func isDirectory(u repo.User) bool { return u.AuthSource == "ldap" }
