package crypto

import (
	"crypto/rand"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/nacl/box"
)

// Cifrado de sobre de los sellados (SPEC §6.3, ADR-0004): cada documento tiene su DEK, que cifra
// el contenido y se guarda solo envuelta, una vez por cada forma de abrirlo.

// DEKSize y SaltSize: 32 bytes de llave (XChaCha20-Poly1305) y 16 de sal para Argon2id.
const (
	DEKSize  = 32
	SaltSize = 16
)

// Parámetros de Argon2id para la contraseña del documento (§6.4): 64 MiB, 3 pasadas, 1 hilo.
// Hacen cara cada prueba de fuerza bruta sobre una copia robada de la BD; el tope de intentos
// del servidor cubre la que se hace por la API.
const (
	kdfTime    = 3
	kdfMemory  = 64 * 1024
	kdfThreads = 1
)

// NewDEK es la llave de un documento nuevo.
func NewDEK() ([]byte, error) { return random(DEKSize) }

// NewSalt es la sal de una contraseña de documento.
func NewSalt() ([]byte, error) { return random(SaltSize) }

// PasswordKEK deriva de la contraseña del documento la llave que envuelve su DEK.
func PasswordKEK(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, kdfTime, kdfMemory, kdfThreads, DEKSize)
}

func random(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// Bóveda (§6.3): un par X25519 por persona. La privada se guarda cifrada con la clave de bóveda
// (Argon2id, los mismos parámetros); la pública basta para darle acceso a un sellado sin que
// esté conectada: la DEK se envuelve para ella con una caja anónima (sealed box).

// NewVault crea el par de claves y devuelve la pública y la privada cifrada con la clave de
// bóveda. aad ata la privada a su dueño: copiada a otra cuenta no abre.
func NewVault(vaultKey string, aad []byte) (pub, privEnc, salt []byte, err error) {
	pk, sk, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	if salt, err = NewSalt(); err != nil {
		return nil, nil, nil, err
	}
	if privEnc, err = Seal(PasswordKEK(vaultKey, salt), sk[:], aad); err != nil {
		return nil, nil, nil, err
	}
	return pk[:], privEnc, salt, nil
}

// WrapForVault envuelve la DEK para la dueña de pub.
func WrapForVault(pub, dek []byte) ([]byte, error) {
	if len(pub) != 32 {
		return nil, ErrOpen
	}
	return box.SealAnonymous(nil, dek, (*[32]byte)(pub), rand.Reader)
}

// OpenWithVault abre una DEK envuelta para la bóveda: con la clave de bóveda descifra la
// privada y con ella la caja. Una clave equivocada da ErrOpen, igual que una caja ajena.
func OpenWithVault(vaultKey string, salt, privEnc, aad, pub, wrapped []byte) ([]byte, error) {
	sk, err := Open(PasswordKEK(vaultKey, salt), privEnc, aad)
	if err != nil {
		return nil, err
	}
	if len(sk) != 32 || len(pub) != 32 {
		return nil, ErrOpen
	}
	dek, ok := box.OpenAnonymous(nil, wrapped, (*[32]byte)(pub), (*[32]byte)(sk))
	if !ok {
		return nil, ErrOpen
	}
	return dek, nil
}
