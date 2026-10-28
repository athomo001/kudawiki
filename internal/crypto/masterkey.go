// Package crypto maneja la MASTER_KEY; el cifrado de sobre de los sellados llega en F7 (SPEC §6.3).
package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// MasterKeySize son 32 bytes aleatorios (SPEC §6.3).
const MasterKeySize = 32

// GenerateMasterKey crea la llave una sola vez: O_EXCL impide pisar una existente,
// porque perderla deja ilegibles los sellados (SPEC §6.3).
func GenerateMasterKey(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	key := make([]byte, MasterKeySize)
	if _, err := rand.Read(key); err != nil {
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
