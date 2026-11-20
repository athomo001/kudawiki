package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/athomo001/kudawiki/internal/crypto"
	"golang.org/x/crypto/argon2"
)

// Parámetros de SPEC §8.1: Argon2id m=64 MiB, t=3, p=2.
const (
	argonMemory  = 64 * 1024
	argonTime    = 3
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

var (
	ErrPasswordTooShort = errors.New("password_too_short")
	ErrPasswordCommon   = errors.New("password_common")
)

// common_passwords.txt: contraseñas de 10 o más caracteres de las listas de SecLists
// (MIT, github.com/danielmiessler/SecLists: NCSC top 100k y xato-net top 1M), en minúsculas
// y ordenadas por bytes para buscarlas sin cargarlas en un mapa.
//
//go:embed common_passwords.txt
var commonPasswordsRaw string

var commonPasswords = strings.Split(strings.TrimSuffix(commonPasswordsRaw, "\n"), "\n")

// CheckPolicy aplica la política de contraseñas locales (SPEC §8.1) con el mínimo vigente, que
// nunca es menor que MinPasswordLength. La lista de comunes vale siempre, también en desarrollo.
func CheckPolicy(password string, minLength int) error {
	if utf8.RuneCountInString(password) < max(minLength, MinPasswordLength) {
		return ErrPasswordTooShort
	}
	p := strings.ToLower(password)
	if i := sort.SearchStrings(commonPasswords, p); i < len(commonPasswords) && commonPasswords[i] == p {
		return ErrPasswordCommon
	}
	return nil
}

// derive pasa por el tope de Argon2id del proceso (crypto.Argon2ID), el mismo de los sellados:
// cada hash reserva 64 MiB, y la espera termina si quien pidió se fue o si tarda demasiado (QA-04).
func derive(ctx context.Context, password string, salt []byte, t, m uint32, p uint8, keyLen uint32) ([]byte, error) {
	return crypto.Argon2ID(ctx, []byte(password), salt, t, m, p, keyLen)
}

// HashPassword devuelve el formato PHC estándar, que guarda los parámetros junto al hash
// para poder subirlos más adelante sin invalidar los existentes.
func HashPassword(ctx context.Context, password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := derive(ctx, password, salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// VerifyPassword compara en tiempo constante.
func VerifyPassword(ctx context.Context, encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unsupported password hash")
	}
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, err
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	// #nosec G115 -- largo de un hash Argon2id guardado por nosotros (32 bytes)
	got, err := derive(ctx, password, salt, t, m, p, uint32(len(want)))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
