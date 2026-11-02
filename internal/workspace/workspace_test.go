package workspace

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/athomo001/kudawiki/internal/repo"
)

func TestCreateValidatesSlug(t *testing.T) {
	r := repo.New(dbtest.Open(t))
	s := NewService(r, audit.NewRecorder(r, time.Now, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler))
	ctx := context.Background()
	for slug, want := range map[string]error{
		"ti":            nil,
		"recursos-2026": nil,
		"t":             ErrInvalidArea,
		"TI":            ErrInvalidArea,
		"-ti":           ErrInvalidArea,
		"ti/../x":       ErrInvalidArea,
	} {
		if _, err := s.Create(ctx, slug, "Nombre"); !errors.Is(err, want) {
			t.Errorf("Create(%q) = %v, want %v", slug, err, want)
		}
	}
	if _, err := s.Create(ctx, "ti", "Otra"); !errors.Is(err, ErrSlugTaken) {
		t.Errorf("duplicate slug: %v", err)
	}
}

// §8.3.2: lo que da el directorio y lo que se da a mano conviven; el rol efectivo es el más alto
// y cada lado solo toca lo suyo.
func TestDirectoryMemberships(t *testing.T) {
	r := repo.New(dbtest.Open(t))
	s := NewService(r, audit.NewRecorder(r, time.Now, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler))
	ctx := context.Background()
	ti, err := s.Create(ctx, "ti", "TI")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []repo.User{{ID: "admin", Email: "admin@acme.cl"}, {ID: "ana", Email: "ana@acme.cl"}, {ID: "super", Email: "super@acme.cl", IsGlobalAdmin: true}} {
		u.FullName, u.IsActive, u.Locale = u.ID, true, "es"
		if err := r.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	sc := repo.Scope{WsID: ti.ID}
	if err := r.SetMember(ctx, sc, "admin", "admin"); err != nil {
		t.Fatal(err)
	}
	areaAdmin, super := Actor{UserID: "admin"}, Actor{UserID: "super", Superadmin: true}
	source := func() repo.MemberSource {
		m, err := r.MemberSourceOf(ctx, sc, "ana")
		if err != nil {
			t.Fatal(err)
		}
		return m
	}

	if err := s.SyncDirectory(ctx, "ana", []repo.DirectoryGrant{{WsID: ti.ID, Role: "viewer", Group: "GG-TI"}}); err != nil {
		t.Fatal(err)
	}
	if m := source(); m.Role != "viewer" || m.LDAPRole != "viewer" || m.ManualRole != "" {
		t.Fatalf("after sync = %+v", m)
	}
	// Solo del directorio: el admin del área no lo cambia ni lo quita.
	if err := s.SetMember(ctx, areaAdmin, sc, "ana@acme.cl", "editor"); !errors.Is(err, ErrManagedByDirectory) {
		t.Errorf("area admin sets = %v", err)
	}
	if err := s.RemoveMember(ctx, areaAdmin, sc, "ana"); !errors.Is(err, ErrManagedByDirectory) {
		t.Errorf("area admin removes = %v", err)
	}
	// El superadmin puede sumarle un rol a mano; vale el más alto.
	if err := s.SetMember(ctx, super, sc, "ana@acme.cl", "editor"); err != nil {
		t.Fatal(err)
	}
	if m := source(); m.Role != "editor" || m.ManualRole != "editor" || m.LDAPRole != "viewer" {
		t.Errorf("manual on top = %+v", m)
	}
	// El directorio sube su rol por encima del manual, y lo baja otra vez sin tocar el manual.
	if err := s.SyncDirectory(ctx, "ana", []repo.DirectoryGrant{{WsID: ti.ID, Role: "admin", Group: "GG-TI-Admins"}}); err != nil {
		t.Fatal(err)
	}
	if m := source(); m.Role != "admin" || m.ManualRole != "editor" {
		t.Errorf("directory raises = %+v", m)
	}
	if err := s.SyncDirectory(ctx, "ana", nil); err != nil {
		t.Fatal(err)
	}
	if m := source(); m.Role != "editor" || m.LDAPRole != "" || m.ManualRole != "editor" {
		t.Errorf("directory leaves, manual stays = %+v", m)
	}
	// Quitar a quien está también por un grupo le quita solo lo manual.
	if err := s.SyncDirectory(ctx, "ana", []repo.DirectoryGrant{{WsID: ti.ID, Role: "viewer", Group: "GG-TI"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveMember(ctx, super, sc, "ana"); err != nil {
		t.Fatal(err)
	}
	if m := source(); m.Role != "viewer" || m.ManualRole != "" || m.LDAPRole != "viewer" {
		t.Errorf("remove keeps the directory role = %+v", m)
	}
	if err := s.SyncDirectory(ctx, "ana", nil); err != nil {
		t.Fatal(err)
	}
	if m := source(); m.Role != "" {
		t.Errorf("out of every group = %+v", m)
	}

	// Desanclar (§8.3.2): solo el superadmin; lo del grupo pasa a ser manual y el directorio deja
	// de quitarlo.
	if err := s.SyncDirectory(ctx, "ana", []repo.DirectoryGrant{{WsID: ti.ID, Role: "editor", Group: "GG-TI"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Detach(ctx, areaAdmin, sc, "ana"); !errors.Is(err, ErrForbidden) {
		t.Errorf("area admin detaches = %v", err)
	}
	if err := s.Detach(ctx, super, sc, "ana"); err != nil {
		t.Fatal(err)
	}
	if m := source(); m.Role != "editor" || m.ManualRole != "editor" || m.LDAPRole != "" {
		t.Errorf("detached = %+v", m)
	}
	if err := s.SyncDirectory(ctx, "ana", nil); err != nil {
		t.Fatal(err)
	}
	if m := source(); m.Role != "editor" {
		t.Errorf("the directory removed a detached membership = %+v", m)
	}
	if err := s.Detach(ctx, super, sc, "ana"); err != nil {
		t.Errorf("detach without a directory role = %v", err)
	}
}
