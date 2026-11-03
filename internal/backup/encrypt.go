package backup

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/chacha20poly1305"
)

// La copia que sale del servidor va cifrada con una llave de respaldo propia, no con la
// MASTER_KEY (§13.3, §21.2.4): quien restaura en otra máquina necesita esta llave y, para los
// sellados, la MASTER_KEY; ninguna de las dos viaja con el respaldo.

// KeySize son 32 bytes aleatorios.
const KeySize = chacha20poly1305.KeySize

// encMagic abre cada archivo cifrado; el número es la versión del formato.
var encMagic = []byte("KWBACKUP1\n")

// chunkSize es cuánto texto va en cada bloque cifrado: se cifra por bloques para no tener el
// respaldo entero en memoria.
const chunkSize = 1 << 20

// EncSuffix es la extensión del respaldo cifrado.
const EncSuffix = ".enc"

// ErrNotEncrypted: el archivo no es un respaldo cifrado de Kudawiki.
var ErrNotEncrypted = errors.New("not an encrypted kudawiki backup")

// ErrBadKey: la llave no descifra el archivo (otra llave, o el archivo se alteró o se cortó).
var ErrBadKey = errors.New("the backup key does not decrypt this file, or the file was altered or cut")

// GenerateKey crea la llave de respaldo una sola vez (O_EXCL), con permisos 0400.
func GenerateKey(path string) error {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400)
	if err != nil {
		return fmt.Errorf("backup key: %w", err)
	}
	_, err = f.Write(key)
	return errors.Join(err, f.Close())
}

// LoadKey lee la llave de respaldo.
func LoadKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("backup key: %w (create it with `kudawiki backup keygen`)", err)
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("backup key %s must be %d bytes, has %d", path, KeySize, len(key))
	}
	return key, nil
}

// Encrypt cifra src con XChaCha20-Poly1305 en bloques de 1 MiB. Cada bloque lleva su número y
// si es el último en el nonce y en los datos asociados: no se pueden reordenar, repetir ni
// cortar bloques sin que Decrypt lo note.
func Encrypt(dst io.Writer, src io.Reader, key []byte) error {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return err
	}
	prefix := make([]byte, aead.NonceSize()-8)
	if _, err := rand.Read(prefix); err != nil {
		return err
	}
	if _, err := dst.Write(append(append([]byte{}, encMagic...), prefix...)); err != nil {
		return err
	}
	buf := make([]byte, chunkSize)
	next := make([]byte, 1)
	var n int
	var counter uint64
	// Se lee un byte de más para saber si el bloque es el último.
	n, err = io.ReadFull(src, buf)
	for {
		last := false
		switch {
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			last = true
		case err != nil:
			return err
		default:
			if _, perr := io.ReadFull(src, next); errors.Is(perr, io.EOF) {
				last = true
			} else if perr != nil {
				return perr
			}
		}
		sealed := aead.Seal(nil, nonce(prefix, counter), buf[:n], chunkAD(counter, last))
		var size [4]byte
		// #nosec G115 -- un bloque mide como mucho 1 MiB más el tag
		binary.BigEndian.PutUint32(size[:], uint32(len(sealed)))
		if _, err := dst.Write(append(size[:], sealed...)); err != nil {
			return err
		}
		if last {
			return nil
		}
		counter++
		buf[0] = next[0]
		var m int
		m, err = io.ReadFull(src, buf[1:])
		n = m + 1
	}
}

// Decrypt deshace Encrypt y falla si falta, sobra o cambió cualquier bloque.
func Decrypt(dst io.Writer, src io.Reader, key []byte) error {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return err
	}
	head := make([]byte, len(encMagic)+aead.NonceSize()-8)
	if _, err := io.ReadFull(src, head); err != nil || !bytes.Equal(head[:len(encMagic)], encMagic) {
		return ErrNotEncrypted
	}
	prefix := head[len(encMagic):]
	var counter uint64
	for {
		var size [4]byte
		if _, err := io.ReadFull(src, size[:]); err != nil {
			return ErrBadKey // se cortó antes del último bloque
		}
		n := binary.BigEndian.Uint32(size[:])
		// #nosec G115 -- el overhead del AEAD es 16
		if n > chunkSize+uint32(aead.Overhead()) {
			return ErrBadKey
		}
		sealed := make([]byte, n)
		if _, err := io.ReadFull(src, sealed); err != nil {
			return ErrBadKey
		}
		// Se prueba primero como bloque intermedio y después como el último.
		last := false
		plain, err := aead.Open(nil, nonce(prefix, counter), sealed, chunkAD(counter, false))
		if err != nil {
			if plain, err = aead.Open(nil, nonce(prefix, counter), sealed, chunkAD(counter, true)); err != nil {
				return ErrBadKey
			}
			last = true
		}
		if _, err := dst.Write(plain); err != nil {
			return err
		}
		if last {
			if _, err := src.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
				return ErrBadKey // hay algo después del último bloque
			}
			return nil
		}
		counter++
	}
}

func nonce(prefix []byte, counter uint64) []byte {
	n := make([]byte, len(prefix)+8)
	copy(n, prefix)
	binary.BigEndian.PutUint64(n[len(prefix):], counter)
	return n
}

func chunkAD(counter uint64, last bool) []byte {
	ad := make([]byte, 9)
	binary.BigEndian.PutUint64(ad, counter)
	if last {
		ad[8] = 1
	}
	return ad
}

// EncryptFile cifra src en src + ".enc" (sin pisar uno que exista) y devuelve esa ruta.
func EncryptFile(src string, key []byte) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	dst := src + EncSuffix
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	err = Encrypt(out, in, key)
	if err = errors.Join(err, out.Close()); err != nil {
		os.Remove(dst)
		return "", err
	}
	return dst, nil
}

// DecryptFile descifra src en dst (que no puede existir).
func DecryptFile(src, dst string, key []byte) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	err = Decrypt(out, in, key)
	if err = errors.Join(err, out.Close()); err != nil {
		os.Remove(dst)
		return err
	}
	return nil
}
