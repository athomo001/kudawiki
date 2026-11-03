// Package crypto maneja la MASTER_KEY y los secretos de configuración que cifra (SPEC §1, §10.2);
// el cifrado de sobre de los sellados llega en F7 (§6.3).
package crypto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// MasterKeySize son 32 bytes aleatorios (SPEC §6.3).
const MasterKeySize = 32

// MasterKey es la llave con su versión (§6.5). Cada fila cifrada guarda la versión con que se
// cifró; el servidor la sabe al arrancar, comparando KeyCheck con master_key_versions.
type MasterKey struct {
	Key     []byte
	Version int
}

// KeyCheck identifica la llave sin guardarla: un HMAC de una constante. Con él la base dice si
// una llave es la suya, y de qué versión, sin poder descifrar nada (§10).
func KeyCheck(key []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("kudawiki master key check"))
	return m.Sum(nil)
}

// GenerateMasterKey crea la llave una sola vez: O_EXCL impide pisar una existente,
// porque perderla deja ilegibles los sellados (SPEC §6.3).
func GenerateMasterKey(path string) error {
	key := make([]byte, MasterKeySize)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	return WriteMasterKey(path, key)
}

// WriteMasterKey escribe la llave en un archivo nuevo, de solo lectura para su dueño. Nunca pisa
// uno que ya existe.
func WriteMasterKey(path string, key []byte) error {
	if len(key) != MasterKeySize {
		return errors.New("master key must be exactly 32 bytes")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400)
	if err != nil {
		return fmt.Errorf("create master key: %w", err)
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}

// LoadMasterKey falla si la llave no existe o no mide 32 bytes: el servicio no arranca sin ella.
func LoadMasterKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read master key: %w", err)
	}
	if len(key) != MasterKeySize {
		return nil, errors.New("master key must be exactly 32 bytes")
	}
	return key, nil
}
