package backups

import (
	"context"
	"errors"
	"testing"

	"github.com/athomo001/kudawiki/internal/crypto"
)

// Con un respaldo en curso no arranca otro ni un borrado: el panel recibe backup_running.
func TestOneAtATime(t *testing.T) {
	s := NewService(nil, nil, testKey(), Options{})
	if !s.begin("backup") {
		t.Fatal("begin")
	}
	if err := s.RunNow(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("run now: %v", err)
	}
	if err := s.Reset(context.Background(), "u1", ConfirmPhrase); !errors.Is(err, ErrBusy) {
		t.Errorf("reset: %v", err)
	}
	// La frase se revisa antes: equivocarse no dice si hay algo corriendo.
	if err := s.Reset(context.Background(), "u1", "borrar todo"); !errors.Is(err, ErrConfirm) {
		t.Errorf("wrong phrase: %v", err)
	}
	s.end(nil)
	if !s.begin("reset") {
		t.Error("begin after end")
	}
}

func testKey() crypto.MasterKey { return crypto.MasterKey{Key: make([]byte, 32), Version: 1} }
