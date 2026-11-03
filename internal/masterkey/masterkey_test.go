package masterkey

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/auth"
	"github.com/athomo001/kudawiki/internal/crypto"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/sealed"
	"github.com/athomo001/kudawiki/internal/workspace"
	"github.com/oklog/ulid/v2"
)

type fixture struct {
	ctx     context.Context
	store   *repo.Repo
	keyFile string
	key     []byte
	plain   map[string][]byte // lo que hay cifrado, por Kind:ID
}

// newFixture es una instalación con la llave de siempre (versión 1) y un secreto de cada tipo:
// la DEK de un sellado, un secreto TOTP, la contraseña SMTP y un correo en cola.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{ctx: context.Background(), store: repo.New(dbtest.Open(t)), keyFile: filepath.Join(t.TempDir(), "master.key"), plain: map[string][]byte{}}
	if err := crypto.GenerateMasterKey(f.keyFile); err != nil {
		t.Fatal(err)
	}
	f.key, _ = crypto.LoadMasterKey(f.keyFile)
	if _, err := Load(f.ctx, f.store, f.keyFile); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	ws, err := workspace.NewService(f.store, audit.NewRecorder(f.store, time.Now, log), log).Create(f.ctx, "ti", "TI")
	if err != nil {
		t.Fatal(err)
	}
	u := repo.User{ID: ulid.Make().String(), FullName: "Ana", PasswordHash: "x", IsActive: true, Locale: "es"}
	u.Email = u.ID + "@empresa.cl"
	if err := f.store.CreateUser(f.ctx, u); err != nil {
		t.Fatal(err)
	}
	seal := func(kind, id string, plain, aad []byte) []byte {
		f.plain[kind+":"+id] = plain
		enc, err := crypto.Seal(f.key, plain, aad)
		if err != nil {
			t.Fatal(err)
		}
		return enc
	}
	page := ulid.Make().String()
	err = f.store.CreateSealed(f.ctx, repo.Scope{WsID: ws.ID, MaxRank: 3}, repo.NewSealed{ID: page, Slug: "legal/acta", AuthorID: u.ID, Payload: []byte("x"),
		Keys: []repo.SealedKey{{Type: "master", Wrapped: seal("sealed", page, []byte("una DEK de 32 bytes............."), sealed.MasterAAD(page)), KeyVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetPendingTOTP(f.ctx, u.ID, seal("totp", u.ID, []byte("JBSWY3DPEHPK3PXP"), auth.TOTPAAD(u.ID)), 1); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SaveSiteSettings(f.ctx, nil, map[string]repo.SealedSetting{
		"smtp.password": {Enc: seal("setting", "smtp.password", []byte("la del correo"), []byte("smtp.password")), KeyVersion: 1}}, u.ID); err != nil {
		t.Fatal(err)
	}
	mail := ulid.Make().String()
	if err := f.store.EnqueueEmail(f.ctx, repo.OutboxEmail{ID: mail, Template: "invite", PayloadEnc: seal("outbox", mail, []byte(`{"to":"x"}`), []byte(mail)), KeyVersion: 1}, time.Now()); err != nil {
		t.Fatal(err)
	}
	return f
}

// opensWith comprueba que cada secreto sigue diciendo lo mismo, ahora con key y en version.
func (f *fixture) opensWith(t *testing.T, key []byte, version int) {
	t.Helper()
	list, err := f.store.SecretsAt(f.ctx, version, 100)
	if err != nil || len(list) != len(f.plain) {
		t.Fatalf("secrets at version %d: %d of %d, %v", version, len(list), len(f.plain), err)
	}
	for _, s := range list {
		got, err := crypto.Open(key, s.Enc, aad(s))
		if err != nil || !bytes.Equal(got, f.plain[s.Kind+":"+s.ID]) {
			t.Errorf("%s %s: %q %v", s.Kind, s.ID, got, err)
		}
	}
}

func TestLoad(t *testing.T) {
	f := newFixture(t)
	if mk, err := Load(f.ctx, f.store, f.keyFile); err != nil || mk.Version != 1 || !bytes.Equal(mk.Key, f.key) {
		t.Fatalf("load: %+v %v", mk, err)
	}
	// Una llave que no es de esta base: el servidor no arranca con ella.
	other := filepath.Join(t.TempDir(), "other.key")
	crypto.GenerateMasterKey(other)
	if _, err := Load(f.ctx, f.store, other); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("unknown key: %v", err)
	}
	// Una fila cifrada con otra versión es una rotación a medias.
	if err := f.store.AddMasterKeyVersion(f.ctx, 2, crypto.KeyCheck(bytes.Repeat([]byte{2}, 32))); err != nil {
		t.Fatal(err)
	}
	list, _ := f.store.SecretsAt(f.ctx, 1, 1)
	if err := f.store.ResealSecrets(f.ctx, 1, 2, list); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(f.ctx, f.store, f.keyFile); !errors.Is(err, ErrUnfinished) {
		t.Errorf("rows at another version: %v", err)
	}
}

func TestRotate(t *testing.T) {
	f := newFixture(t)
	r, err := Rotate(f.ctx, f.store, f.keyFile, func() (string, error) { return "backup.tar.gz", nil })
	if err != nil {
		t.Fatal(err)
	}
	if r.From != 1 || r.To != 2 || r.Secrets != 4 || r.Backup != "backup.tar.gz" || r.Old != f.keyFile+".v1" {
		t.Errorf("rotation: %+v", r)
	}
	newKey, _ := crypto.LoadMasterKey(f.keyFile)
	if old, _ := os.ReadFile(r.Old); bytes.Equal(newKey, f.key) || !bytes.Equal(old, f.key) {
		t.Fatal("the key file must hold the new key and .v1 the old one")
	}
	if _, err := os.Stat(f.keyFile + ".next"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".next left behind: %v", err)
	}
	f.opensWith(t, newKey, 2)
	if mk, err := Load(f.ctx, f.store, f.keyFile); err != nil || mk.Version != 2 {
		t.Errorf("load after rotating: %+v %v", mk, err)
	}
	if _, err := Load(f.ctx, f.store, r.Old); !errors.Is(err, ErrRetiredKey) {
		t.Errorf("old key: %v", err)
	}
	// Otra rotación sigue la cuenta.
	if r, err := Rotate(f.ctx, f.store, f.keyFile, func() (string, error) { return "", nil }); err != nil || r.From != 2 || r.To != 3 {
		t.Errorf("second rotation: %+v %v", r, err)
	}
}

// TestRotateResume: una rotación cortada a la mitad (la llave nueva en .next y una fila ya
// re-cifrada) no deja arrancar el servidor; correrla de nuevo sigue sin otro respaldo.
func TestRotateResume(t *testing.T) {
	f := newFixture(t)
	newKey := bytes.Repeat([]byte{5}, 32)
	if err := crypto.WriteMasterKey(f.keyFile+".next", newKey); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddMasterKeyVersion(f.ctx, 2, crypto.KeyCheck(newKey)); err != nil {
		t.Fatal(err)
	}
	if _, err := Reseal(f.ctx, cut{f.store}, crypto.MasterKey{Key: f.key, Version: 1}, crypto.MasterKey{Key: newKey, Version: 2}); err == nil {
		t.Fatal("the cut did not happen")
	}
	if _, err := Load(f.ctx, f.store, f.keyFile); !errors.Is(err, ErrUnfinished) {
		t.Fatalf("server start mid-rotation: %v", err)
	}
	r, err := Rotate(f.ctx, f.store, f.keyFile, func() (string, error) { t.Fatal("backup again when resuming"); return "", nil })
	if err != nil || r.From != 1 || r.To != 2 || r.Secrets != 3 || r.Backup != "" {
		t.Fatalf("resume: %+v %v", r, err)
	}
	f.opensWith(t, newKey, 2)
	if mk, err := Load(f.ctx, f.store, f.keyFile); err != nil || mk.Version != 2 {
		t.Errorf("load after resuming: %+v %v", mk, err)
	}
}

// cut re-cifra un lote de una fila y después se corta, como un proceso que muere a la mitad.
type cut struct{ *repo.Repo }

func (c cut) SecretsAt(ctx context.Context, version, _ int) ([]repo.Secret, error) {
	return c.Repo.SecretsAt(ctx, version, 1)
}

func (c cut) ResealSecrets(ctx context.Context, from, to int, s []repo.Secret) error {
	if n, _ := c.Repo.SecretsNotAt(ctx, from); n > 0 {
		return errors.New("cut")
	}
	return c.Repo.ResealSecrets(ctx, from, to, s)
}
