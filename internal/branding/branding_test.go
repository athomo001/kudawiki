package branding

import (
	"context"
	"errors"
	"image/color"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/athomo001/kudawiki/internal/repo"
)

func newService(t *testing.T, fromConfig Patch) (*Service, *repo.Repo, string) {
	t.Helper()
	r := repo.New(dbtest.Open(t))
	ctx := context.Background()
	if err := r.CreateUser(ctx, repo.User{ID: "super", Email: "super@empresa.cl", FullName: "Super", IsGlobalAdmin: true, IsActive: true, Locale: "es"}); err != nil {
		t.Fatal(err)
	}
	s, err := NewService(r, audit.NewRecorder(r, time.Now, slog.New(slog.DiscardHandler)), fromConfig)
	if err != nil {
		t.Fatal(err)
	}
	return s, r, "super"
}

func lastEvent(t *testing.T, r *repo.Repo) repo.AuditEntry {
	t.Helper()
	ev, err := r.AuditEvents(context.Background(), repo.AuditFilter{Limit: 1})
	if err != nil || len(ev) == 0 {
		t.Fatalf("audit: %v", err)
	}
	return ev[0]
}

func TestSiteDefaultsAndUpdate(t *testing.T) {
	s, r, super := newService(t, Patch{})
	ctx := context.Background()
	site, err := s.Site(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if site.Settings != Defaults || len(site.Assets) != 0 || len(site.Locked) != 0 || site.Palette.CSS() != "" {
		t.Fatalf("defaults = %+v", site)
	}

	site, err = s.Update(ctx, super, Patch{Title: new("Wiki ACME"), ColorPrimary: new("#C8102E"), LoginMessage: new("Uso exclusivo\nde ACME"),
		ShowPoweredBy: new(false)})
	if err != nil {
		t.Fatal(err)
	}
	if site.Title != "Wiki ACME" || site.ColorPrimary != "#c8102e" || site.ShowPoweredBy || site.Palette.Light["--kw-accent"] != "#c8102e" {
		t.Errorf("after update = %+v", site)
	}
	// La auditoría dice qué claves cambiaron, nunca sus valores (§15.1.5).
	e := lastEvent(t, r)
	if e.Action != "site.branding_update" || !strings.Contains(e.Meta, `"color_primary","login_message","show_powered_by","title"`) || strings.Contains(e.Meta, "ACME") {
		t.Errorf("audit = %s %s", e.Action, e.Meta)
	}

	// "" vuelve al valor por defecto; un Patch vacío no cambia nada ni se registra.
	site, _ = s.Update(ctx, super, Patch{Title: new(""), ColorPrimary: new("")})
	if site.Title != "Kudawiki" || site.ColorPrimary != DefaultPrimary {
		t.Errorf("reset = %+v", site.Settings)
	}
	before := lastEvent(t, r).ID
	s.Update(ctx, super, Patch{})
	if lastEvent(t, r).ID != before {
		t.Error("an empty patch was audited")
	}

	for name, p := range map[string]Patch{
		"color con nombre":  {ColorHeader: new("red")},
		"color con css":     {ColorSidebar: new("#123456;}body{x")},
		"título largo":      {Title: new(strings.Repeat("a", 41))},
		"título con salto":  {Title: new("Wiki\nACME")},
		"empresa con tab":   {Company: new("ACME\tS.A.")},
		"mensaje muy largo": {LoginMessage: new(strings.Repeat("x", 501))},
	} {
		if _, err := s.Update(ctx, super, p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
}

// Lo que fija el archivo gana, queda bloqueado en el panel, y uno mal escrito no deja arrancar.
func TestConfigWins(t *testing.T) {
	s, _, super := newService(t, Patch{Title: new("Wiki de Config"), ColorHeader: new("#FFFFFF")})
	ctx := context.Background()
	if _, err := s.Update(ctx, super, Patch{Title: new("Otro")}); !errors.Is(err, ErrLocked) {
		t.Errorf("locked title: %v", err)
	}
	site, err := s.Update(ctx, super, Patch{Company: new("ACME S.A.")})
	if err != nil {
		t.Fatal(err)
	}
	if site.Title != "Wiki de Config" || site.ColorHeader != "#ffffff" || site.Company != "ACME S.A." ||
		!slices.Equal(site.Locked, []string{"color_header", "title"}) {
		t.Errorf("site = %+v", site)
	}
	if _, err := NewService(nil, nil, Patch{ColorPrimary: new("azul")}); err == nil {
		t.Error("a bad color in the config file was accepted")
	}
}

func TestAssets(t *testing.T) {
	s, r, super := newService(t, Patch{})
	ctx := context.Background()
	if _, err := s.File(ctx, "favicon.ico"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("no assets: %v", err)
	}
	if _, err := s.Upload(ctx, super, "fondo", []byte(capasSVG)); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("unknown kind: %v", err)
	}

	// Sin favicon, los íconos salen del logo.
	if _, err := s.Upload(ctx, super, "logo", []byte(capasSVG)); err != nil {
		t.Fatal(err)
	}
	site, _ := s.Site(ctx)
	fromLogo, err := s.File(ctx, "icon-180.png")
	if err != nil || site.Assets["logo"] == "" || site.Assets["icons"] == "" || site.Assets["favicon"] != "" {
		t.Fatalf("icons from logo: %v, %+v", err, site.Assets)
	}
	e := lastEvent(t, r)
	if e.Action != "site.asset_upload" || e.TargetID != "logo" || !strings.Contains(e.Meta, `"sha256"`) {
		t.Errorf("audit = %+v", e)
	}

	// Con favicon, mandan los suyos.
	if _, err := s.Upload(ctx, super, "favicon", pngOf(t, 64, 64, color.RGBA{0xc8, 0x10, 0x2e, 0xff})); err != nil {
		t.Fatal(err)
	}
	fav, _ := s.File(ctx, "icon-180.png")
	if fav.SHA256 == fromLogo.SHA256 || fav.Kind != "favicon" {
		t.Error("the favicon did not replace the icons from the logo")
	}
	if logo, _ := s.File(ctx, "logo"); logo.MimeType != "image/svg+xml" || !strings.HasPrefix(string(logo.Data), "<svg") {
		t.Errorf("logo = %s %.20s", logo.MimeType, logo.Data)
	}

	// Al quitarlo vuelven los del logo; quitar lo que no está es un 404.
	if err := s.DeleteAsset(ctx, "favicon"); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.File(ctx, "icon-180.png"); again.SHA256 != fromLogo.SHA256 {
		t.Error("icons did not fall back to the logo")
	}
	if err := s.DeleteAsset(ctx, "favicon"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
	if e := lastEvent(t, r); e.Action != "site.asset_delete" {
		t.Errorf("audit = %s", e.Action)
	}
}
