package crypto

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Con los lugares ocupados, Argon2ID no calcula: devuelve el error del pedido cancelado o, pasado
// kdfWait, ErrBusy. Al liberarse un lugar vuelve a derivar (QA-03, QA-04).
func TestArgon2IDLimit(t *testing.T) {
	for range cap(kdfSlots) {
		kdfSlots <- struct{}{}
	}
	release := func() {
		for range cap(kdfSlots) {
			<-kdfSlots
		}
	}
	defer func() {
		if len(kdfSlots) > 0 {
			release()
		}
	}()
	salt := make([]byte, SaltSize)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Argon2ID(ctx, []byte("x"), salt, 1, 64, 1, 32); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled request while full: %v, want context.Canceled", err)
	}

	old := kdfWait
	kdfWait = 20 * time.Millisecond
	defer func() { kdfWait = old }()
	if _, err := Argon2ID(context.Background(), []byte("x"), salt, 1, 64, 1, 32); !errors.Is(err, ErrBusy) {
		t.Errorf("full for longer than kdfWait: %v, want ErrBusy", err)
	}

	release()
	if k, err := Argon2ID(context.Background(), []byte("x"), salt, 1, 64, 1, 32); err != nil || len(k) != 32 {
		t.Errorf("with room again: %d bytes, %v", len(k), err)
	}
	if len(kdfSlots) != 0 {
		t.Errorf("the slot was not given back: %d in use", len(kdfSlots))
	}
}
