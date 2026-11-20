package crypto

import (
	"context"
	"errors"
	"time"

	"golang.org/x/crypto/argon2"
)

// kdfSlots es el tope de derivaciones Argon2id simultáneas de todo el proceso: las contraseñas de
// las cuentas (§8.1) y las de los sellados y la bóveda (§6.4) reservan 64 MiB cada una, y con el
// contenedor en 512 MiB (§13.11) un puñado sin tope lo mata por memoria. Uno solo para todos: con
// dos topes separados, la suma de los dos ya no cabe.
var kdfSlots = make(chan struct{}, 4)

// kdfWait es cuánto se espera un lugar. Una inundación de logins con correos distintos no frena en
// el tope por IP+email (§8.2): sin plazo, quien entra de verdad esperaría detrás sin límite.
// Variable solo para que el test no espere 10 s.
var kdfWait = 10 * time.Second

// ErrBusy: todos los lugares siguen ocupados después de kdfWait. Su texto es el código que la
// interfaz ya traduce como "demasiados intentos, espera un momento".
var ErrBusy = errors.New("rate_limited")

// Argon2ID deriva la llave cuando hay lugar. Si quien pidió se va (ctx) o el lugar no llega a
// tiempo, no calcula nada: no tiene sentido gastar 64 MiB en una respuesta que nadie lee.
func Argon2ID(ctx context.Context, password, salt []byte, t, m uint32, p uint8, keyLen uint32) ([]byte, error) {
	wait := time.NewTimer(kdfWait)
	defer wait.Stop()
	select {
	case kdfSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-wait.C:
		return nil, ErrBusy
	}
	defer func() { <-kdfSlots }()
	return argon2.IDKey(password, salt, t, m, p, keyLen), nil
}
