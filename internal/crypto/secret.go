package crypto

import (
	"crypto/rand"
	"errors"

	"golang.org/x/crypto/chacha20poly1305"
)

// MasterKeyVersion es la versión de la MASTER_KEY con que se cifra hoy; cada fila guarda la
// suya para que la rotación (§6.5) sepa qué le falta re-cifrar.
const MasterKeyVersion = 1

var ErrOpen = errors.New("secret cannot be opened")

// Seal cifra un secreto de configuración con la MASTER_KEY (XChaCha20-Poly1305, §6.3). El nonce
// es aleatorio y va al frente. aad ata el cifrado a su lugar (el nombre del ajuste, el id de la
// fila): copiar la columna a otra fila no lo deja abrirse ahí.
func Seal(key, plaintext, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize(), aead.NonceSize()+len(plaintext)+aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plaintext, aad), nil
}

// Open descifra lo que cifró Seal; falla igual si la llave, el aad o los bytes no calzan.
func Open(key, box, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	if len(box) < aead.NonceSize()+aead.Overhead() {
		return nil, ErrOpen
	}
	out, err := aead.Open(nil, box[:aead.NonceSize()], box[aead.NonceSize():], aad)
	if err != nil {
		return nil, ErrOpen
	}
	return out, nil
}
