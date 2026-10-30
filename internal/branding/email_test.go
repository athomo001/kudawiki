package branding

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/athomo001/kudawiki/internal/repo"
)

// El logo del correo es un PNG que entra en 400×80, sin deformarse, y cambia con el original.
func TestEmailLogo(t *testing.T) {
	s, _, super := newService(t, Patch{})
	ctx := context.Background()
	if _, err := s.EmailLogo(ctx); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("without logo: %v", err)
	}
	size := func() image.Point {
		t.Helper()
		data, err := s.EmailLogo(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return image.Pt(cfg.Width, cfg.Height)
	}
	if _, err := s.Upload(ctx, super, "logo", pngOf(t, 1000, 100, color.Black)); err != nil {
		t.Fatal(err)
	}
	if got := size(); got != image.Pt(400, 40) {
		t.Errorf("wide logo = %v, want 400×40", got)
	}
	if _, err := s.Upload(ctx, super, "logo", pngOf(t, 200, 200, color.Black)); err != nil {
		t.Fatal(err)
	}
	if got := size(); got != image.Pt(80, 80) {
		t.Errorf("square logo after replacing = %v, want 80×80", got)
	}
}

func TestEmailColors(t *testing.T) {
	s, _, super := newService(t, Patch{})
	ctx := context.Background()
	site, _ := s.Site(ctx)
	if c := site.EmailColors(); c.Button != DefaultPrimary || c.OnButton == "" || c.Link == "" {
		t.Errorf("defaults = %+v", c)
	}
	site, _ = s.Update(ctx, super, Patch{ColorPrimary: new("#ffd700")})
	if c := site.EmailColors(); c.Button != "#ffd700" || c.OnButton == "#fff" || c.Link == "#ffd700" {
		t.Errorf("a light primary needs dark text and a darker link: %+v", c)
	}
}
