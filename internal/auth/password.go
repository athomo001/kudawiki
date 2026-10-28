package auth

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Parámetros de SPEC §8.1: Argon2id m=64 MiB, t=3, p=2.
const (
	argonMemory  = 64 * 1024
	argonTime    = 3
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16

	MinPasswordLength = 10
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

// CheckPolicy aplica la política de contraseñas locales (SPEC §8.1). relaxed solo vale en una
// instalación de desarrollo (base_url en localhost): acepta cualquier contraseña no vacía.
func CheckPolicy(password string, relaxed bool) error {
	if password == "" {
		return ErrPasswordTooShort
	}
	if relaxed {
		return nil
	}
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	p := strings.ToLower(password)
	if i := sort.SearchStrings(commonPasswords, p); i < len(commonPasswords) && commonPasswords[i] == p {
		return ErrPasswordCommon
	}
	return nil
}

// hashSlots limita los hashes simultáneos: cada uno reserva 64 MiB y sin tope
// un puñado de logins concurrentes agota la memoria del contenedor.
var hashSlots = make(chan struct{}, 4)

func derive(password string, salt []byte, t, m uint32, p uint8, keyLen uint32) []byte {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	return argon2.IDKey([]byte(password), salt, t, m, p, keyLen)
}

// HashPassword devuelve el formato PHC estándar, que guarda los parámetros junto al hash
// para poder subirlos más adelante sin invalidar los existentes.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		enc.EncodeToString(salt), enc.EncodeToString(derive(password, salt, argonTime, argonMemory, argonThreads, argonKeyLen))), nil
}

// VerifyPassword compara en tiempo constante.
func VerifyPassword(encoded, password string) (bool, error) {
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
	got := derive(password, salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
