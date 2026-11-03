package backup

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"
)

// §13.3: la copia cifrada vuelve igual, de cualquier tamaño (vacía, menos de un bloque, justo un
// bloque, varios); con otra llave, un byte cambiado, un bloque de menos o algo agregado al final,
// Decrypt falla.
func TestEncryptRoundTripAndTamper(t *testing.T) {
	key := bytes.Repeat([]byte{1}, KeySize)
	for _, size := range []int{0, 10, chunkSize, chunkSize + 1, 3*chunkSize - 7} {
		plain := make([]byte, size)
		rand.Read(plain)
		var enc bytes.Buffer
		if err := Encrypt(&enc, bytes.NewReader(plain), key); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := Decrypt(&out, bytes.NewReader(enc.Bytes()), key); err != nil || !bytes.Equal(out.Bytes(), plain) {
			t.Fatalf("size %d: round trip failed: %v", size, err)
		}
		if bytes.Contains(enc.Bytes(), plain[:min(size, 64)]) && size > 0 {
			t.Errorf("size %d: plaintext visible in the encrypted file", size)
		}
		b := enc.Bytes()
		cases := map[string][]byte{
			"flipped byte": func() []byte { c := bytes.Clone(b); c[len(c)-5] ^= 1; return c }(),
			"cut":          b[:len(b)-3],
			"appended":     append(bytes.Clone(b), 0),
		}
		if size > chunkSize {
			// Sin el último bloque: el penúltimo no está marcado como último.
			cases["missing last chunk"] = b[:len(encMagic)+16+4+chunkSize+16]
		}
		for name, tampered := range cases {
			if err := Decrypt(&bytes.Buffer{}, bytes.NewReader(tampered), key); !errors.Is(err, ErrBadKey) {
				t.Errorf("size %d, %s: %v", size, name, err)
			}
		}
		if err := Decrypt(&bytes.Buffer{}, bytes.NewReader(b), bytes.Repeat([]byte{2}, KeySize)); !errors.Is(err, ErrBadKey) {
			t.Errorf("size %d, wrong key: %v", size, err)
		}
	}
	if err := Decrypt(&bytes.Buffer{}, bytes.NewReader([]byte("not a backup")), key); !errors.Is(err, ErrNotEncrypted) {
		t.Errorf("plain file: %v", err)
	}
}
