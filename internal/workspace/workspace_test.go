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
	s := NewService(r, audit.NewRecorder(r, time.Now, slog.New(slog.DiscardHandler)))
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
