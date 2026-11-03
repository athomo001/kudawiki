// Package masterkey es la versión de la MASTER_KEY y su rotación (SPEC §6.5). La llave vive en un
// archivo fuera de la base; la base guarda solo su KeyCheck por versión, y cada fila cifrada la
// versión con que se cifró. Rotar re-cifra esas filas con una llave nueva; el contenido de los
// sellados no se toca, porque sus DEK no cambian.
package masterkey

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/athomo001/kudawiki/internal/auth"
	"github.com/athomo001/kudawiki/internal/crypto"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/sealed"
)

var (
	ErrUnknownKey = errors.New("the master key does not belong to this database")
	ErrRetiredKey = errors.New("this master key was replaced by a rotation: use the current one")
	ErrUnfinished = errors.New("a master key rotation is unfinished: run `kudawiki masterkey rotate` again")
	ErrNoVersions = errors.New("the database has no master key versions yet: start the server once")
)

// batchSize: filas por transacción al re-cifrar. Cortar a la mitad deja lotes enteros hechos.
const batchSize = 200

type store interface {
	MasterKeyVersions(ctx context.Context) ([]repo.MasterKeyVersion, error)
	AddMasterKeyVersion(ctx context.Context, version int, check []byte) error
	RetireMasterKeyVersion(ctx context.Context, version int) error
	SecretsAt(ctx context.Context, version, limit int) ([]repo.Secret, error)
	SecretsNotAt(ctx context.Context, version int) (int, error)
	ResealSecrets(ctx context.Context, from, to int, secrets []repo.Secret) error
	TruncateWAL(ctx context.Context) error
}

// Load lee la llave del archivo y dice su versión. El servidor no arranca con una llave que no es
// de esta base, con una retirada, ni con una rotación a medias: cifraría con una versión y leería
// filas de otra.
func Load(ctx context.Context, s store, keyFile string) (crypto.MasterKey, error) {
	if _, err := os.Stat(nextFile(keyFile)); err == nil {
		return crypto.MasterKey{}, ErrUnfinished
	}
	key, err := crypto.LoadMasterKey(keyFile)
	if err != nil {
		return crypto.MasterKey{}, err
	}
	mk, err := identify(ctx, s, key, true)
	if err != nil {
		return mk, err
	}
	if n, err := s.SecretsNotAt(ctx, mk.Version); err != nil {
		return mk, err
	} else if n > 0 {
		return mk, ErrUnfinished
	}
	return mk, nil
}

// identify busca la versión de la llave. Con seed, una base sin versiones es de antes de F7.4:
// todo lo que tiene se cifró con esta llave, que pasa a ser la 1.
func identify(ctx context.Context, s store, key []byte, seed bool) (crypto.MasterKey, error) {
	versions, err := s.MasterKeyVersions(ctx)
	if err != nil {
		return crypto.MasterKey{}, err
	}
	check := crypto.KeyCheck(key)
	if len(versions) == 0 {
		if !seed {
			return crypto.MasterKey{}, ErrNoVersions
		}
		return crypto.MasterKey{Key: key, Version: 1}, s.AddMasterKeyVersion(ctx, 1, check)
	}
	for _, v := range versions {
		if bytes.Equal(v.Check, check) {
			if v.Retired {
				return crypto.MasterKey{}, ErrRetiredKey
			}
			return crypto.MasterKey{Key: key, Version: v.Version}, nil
		}
	}
	return crypto.MasterKey{}, ErrUnknownKey
}

// Check dice si key es la llave vigente de la base, para combine (§6.5): la reconstrucción de
// Shamir no sabe si salió bien. Nunca la registra: una llave mal reconstruida no puede pasar a
// ser la de la base.
func Check(ctx context.Context, s store, key []byte) (crypto.MasterKey, error) {
	return identify(ctx, s, key, false)
}

// Rotation es lo que hizo Rotate.
type Rotation struct {
	From, To int
	Secrets  int    // filas re-cifradas en esta corrida
	Old      string // dónde quedó la llave vieja
	Backup   string // el respaldo de antes de empezar; vacío si se retomó una rotación cortada
}

// Rotate cambia la MASTER_KEY con el servidor detenido (lo comprueba quien llama). Es reanudable:
// la llave nueva queda primero en <archivo>.next y cada fila guarda su versión, así que si se
// corta se vuelve a correr y sigue. backup corre antes de empezar (no al retomar). Al terminar,
// la llave vieja queda en <archivo>.v<versión>: los respaldos anteriores la necesitan.
func Rotate(ctx context.Context, s store, keyFile string, backup func() (string, error)) (Rotation, error) {
	oldKey, err := crypto.LoadMasterKey(keyFile)
	if err != nil {
		return Rotation{}, err
	}
	from, err := identify(ctx, s, oldKey, true)
	if err != nil {
		return Rotation{}, err
	}
	var r Rotation
	next := nextFile(keyFile)
	newKey, err := crypto.LoadMasterKey(next)
	if errors.Is(err, os.ErrNotExist) {
		if r.Backup, err = backup(); err != nil {
			return r, fmt.Errorf("backup before rotating: %w", err)
		}
		newKey = make([]byte, crypto.MasterKeySize)
		if _, err := rand.Read(newKey); err != nil {
			return r, err
		}
		err = crypto.WriteMasterKey(next, newKey)
	}
	if err != nil {
		return r, err
	}
	to, err := register(ctx, s, newKey)
	if err != nil {
		return r, err
	}
	r.From, r.To = from.Version, to.Version
	if r.Secrets, err = Reseal(ctx, s, from, to); err != nil {
		return r, err
	}
	if err := s.TruncateWAL(ctx); err != nil {
		return r, err
	}
	// La llave vieja se guarda antes de reemplazarla: si se corta entre los dos pasos, al retomar
	// todavía está en algún lado.
	r.Old = keyFile + ".v" + strconv.Itoa(from.Version)
	if kept, err := os.ReadFile(r.Old); err == nil {
		if !bytes.Equal(kept, oldKey) {
			return r, fmt.Errorf("%s exists and is not the old key", r.Old)
		}
	} else if err := crypto.WriteMasterKey(r.Old, oldKey); err != nil {
		return r, err
	}
	// Es de solo lectura: hay que poder reemplazarla (en Windows, renombrar encima falla si no).
	if err := os.Chmod(keyFile, 0o600); err != nil {
		return r, err
	}
	if err := os.Rename(next, keyFile); err != nil {
		return r, err
	}
	return r, s.RetireMasterKeyVersion(ctx, from.Version)
}

// register es la versión de la llave nueva: la que ya tiene si se retoma, o la siguiente.
func register(ctx context.Context, s store, key []byte) (crypto.MasterKey, error) {
	versions, err := s.MasterKeyVersions(ctx)
	if err != nil {
		return crypto.MasterKey{}, err
	}
	check, last := crypto.KeyCheck(key), 0
	for _, v := range versions {
		if bytes.Equal(v.Check, check) {
			return crypto.MasterKey{Key: key, Version: v.Version}, nil
		}
		last = max(last, v.Version)
	}
	return crypto.MasterKey{Key: key, Version: last + 1}, s.AddMasterKeyVersion(ctx, last+1, check)
}

// Reseal re-cifra con to todo lo que sigue cifrado con from, por lotes. Devuelve cuántas filas.
func Reseal(ctx context.Context, s store, from, to crypto.MasterKey) (int, error) {
	n := 0
	for {
		batch, err := s.SecretsAt(ctx, from.Version, batchSize)
		if err != nil || len(batch) == 0 {
			return n, err
		}
		for i := range batch {
			plain, err := crypto.Open(from.Key, batch[i].Enc, aad(batch[i]))
			if err != nil {
				return n, fmt.Errorf("open %s %s with key version %d: %w", batch[i].Kind, batch[i].ID, from.Version, err)
			}
			if batch[i].Enc, err = crypto.Seal(to.Key, plain, aad(batch[i])); err != nil {
				return n, err
			}
		}
		if err := s.ResealSecrets(ctx, from.Version, to.Version, batch); err != nil {
			return n, err
		}
		n += len(batch)
	}
}

// aad es la de cada lugar, la misma con que lo cifró su dueño: un ajuste y un correo en cola se
// atan a su nombre y a su id (mail, ldap).
func aad(s repo.Secret) []byte {
	switch s.Kind {
	case "sealed":
		return sealed.MasterAAD(s.ID)
	case "totp":
		return auth.TOTPAAD(s.ID)
	}
	return []byte(s.ID)
}

func nextFile(keyFile string) string { return keyFile + ".next" }
