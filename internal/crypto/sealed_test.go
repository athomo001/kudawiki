package crypto

import (
	"bytes"
	"testing"
)

// La DEK envuelta con la contraseña se abre solo con esa contraseña, esa sal y el mismo aad.
func TestPasswordWrap(t *testing.T) {
	dek, _ := NewDEK()
	salt, _ := NewSalt()
	box, err := Seal(PasswordKEK("una contraseña larga", salt), dek, []byte("page-1"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Open(PasswordKEK("una contraseña larga", salt), box, []byte("page-1"))
	if err != nil || !bytes.Equal(got, dek) {
		t.Fatalf("open with the right password: %v", err)
	}
	other, _ := NewSalt()
	for name, key := range map[string][]byte{
		"wrong password": PasswordKEK("otra contraseña larga", salt),
		"wrong salt":     PasswordKEK("una contraseña larga", other),
	} {
		if _, err := Open(key, box, []byte("page-1")); err != ErrOpen {
			t.Errorf("%s: %v, want ErrOpen", name, err)
		}
	}
	if _, err := Open(PasswordKEK("una contraseña larga", salt), box, []byte("page-2")); err != ErrOpen {
		t.Errorf("another page: %v, want ErrOpen", err)
	}
	if a, b := PasswordKEK("x", salt), PasswordKEK("x", salt); !bytes.Equal(a, b) || len(a) != DEKSize {
		t.Error("the KEK is not deterministic or has the wrong size")
	}
}

// La DEK envuelta para una bóveda se abre solo con su clave de bóveda y en su cuenta (aad).
func TestVaultWrap(t *testing.T) {
	pub, priv, salt, err := NewVault("clave de bóveda larga", []byte("user-1"))
	if err != nil {
		t.Fatal(err)
	}
	dek, _ := NewDEK()
	wrapped, err := WrapForVault(pub, dek)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := OpenWithVault("clave de bóveda larga", salt, priv, []byte("user-1"), pub, wrapped); err != nil || !bytes.Equal(got, dek) {
		t.Fatalf("open with the vault key: %v", err)
	}
	if _, err := OpenWithVault("otra clave de bóveda", salt, priv, []byte("user-1"), pub, wrapped); err != ErrOpen {
		t.Errorf("wrong vault key: %v, want ErrOpen", err)
	}
	if _, err := OpenWithVault("clave de bóveda larga", salt, priv, []byte("user-2"), pub, wrapped); err != ErrOpen {
		t.Errorf("another account: %v, want ErrOpen", err)
	}
	otherPub, _, _, _ := NewVault("x", []byte("user-2"))
	foreign, _ := WrapForVault(otherPub, dek)
	if _, err := OpenWithVault("clave de bóveda larga", salt, priv, []byte("user-1"), pub, foreign); err != ErrOpen {
		t.Errorf("a box for someone else: %v, want ErrOpen", err)
	}
}
