package crypto

import (
	"bytes"
	"testing"
)

func TestSealOpen(t *testing.T) {
	key := bytes.Repeat([]byte{7}, MasterKeySize)
	box, err := Seal(key, []byte("clave-smtp"), []byte("smtp.password"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(box, []byte("clave-smtp")) {
		t.Fatal("the plaintext is visible in the sealed box")
	}
	got, err := Open(key, box, []byte("smtp.password"))
	if err != nil || string(got) != "clave-smtp" {
		t.Fatalf("Open = %q, %v", got, err)
	}

	// Mismo texto, otro nonce: dos cifrados nunca coinciden.
	other, _ := Seal(key, []byte("clave-smtp"), []byte("smtp.password"))
	if bytes.Equal(box, other) {
		t.Error("two seals of the same text are equal")
	}
	if _, err := Open(key, box, []byte("auth.ldap.bind_password")); err == nil {
		t.Error("a secret opened under another setting name")
	}
	if _, err := Open(bytes.Repeat([]byte{8}, MasterKeySize), box, []byte("smtp.password")); err == nil {
		t.Error("a secret opened with another key")
	}
	box[len(box)-1] ^= 1
	if _, err := Open(key, box, []byte("smtp.password")); err == nil {
		t.Error("a tampered box opened")
	}
	if _, err := Open(key, box[:5], nil); err == nil {
		t.Error("a truncated box opened")
	}
}
