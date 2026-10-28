package wiki

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/oklog/ulid/v2"
)

func setup(t *testing.T) (*Service, *repo.Repo, repo.Scope, string) {
	t.Helper()
	ctx := context.Background()
	r := repo.New(dbtest.Open(t))
	uid := ulid.Make().String()
	if err := r.CreateUser(ctx, repo.User{ID: uid, Email: "e@x.cl", FullName: "E", IsActive: true, Locale: "es"}); err != nil {
		t.Fatal(err)
	}
	ws := repo.Workspace{ID: ulid.Make().String(), Slug: "ti", Name: "TI"}
	if err := r.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	return NewService(r, audit.NewRecorder(r, time.Now, slog.New(slog.DiscardHandler))), r, repo.Scope{WsID: ws.ID, MaxRank: 2}, uid
}

func TestSaveLifecycle(t *testing.T) {
	s, r, sc, uid := setup(t)
	ctx := context.Background()
	w := repo.PageWrite{Slug: "redes/vpn", Title: "VPN", Markdown: "v1"}

	if err := s.Save(ctx, sc, uid, 0, w); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, sc, uid, 0, w); !errors.Is(err, ErrPathUnavailable) {
		t.Errorf("create twice: %v", err)
	}
	w.Markdown = "v2"
	if err := s.Save(ctx, sc, uid, 1, w); err != nil {
		t.Fatal(err)
	}
	// Quien partió de la revisión 1 llega tarde: conflicto, no pisa el cambio (SPEC §4.1).
	w.Markdown = "v2-otra"
	if err := s.Save(ctx, sc, uid, 1, w); !errors.Is(err, ErrConflict) {
		t.Errorf("stale base: %v", err)
	}
	// Guardar lo mismo no crea revisión (SPEC §4.3).
	w.Markdown = "v2"
	if err := s.Save(ctx, sc, uid, 2, w); err != nil {
		t.Fatal(err)
	}
	if err := s.Revert(ctx, sc, uid, "redes/vpn", 1, 2); err != nil {
		t.Fatal(err)
	}

	p, _ := r.PageBySlug(ctx, sc, "redes/vpn")
	revs, _ := r.Revisions(ctx, sc, p.ID)
	if p.Revision != 3 || p.Markdown != "v1" || len(revs) != 3 || revs[0].Kind != "reverted" {
		t.Errorf("page rev=%d md=%q, revisions=%d first=%q", p.Revision, p.Markdown, len(revs), revs[0].Kind)
	}
}

func TestValidSlug(t *testing.T) {
	for slug, want := range map[string]bool{
		"vpn": true, "redes/vpn/procedimiento-2026": true,
		"": false, "/vpn": false, "vpn/": false, "Redes": false, "redes//vpn": false,
		"redes/../x": false, "con espacio": false, "-x": false,
	} {
		if got := ValidSlug(slug); got != want {
			t.Errorf("ValidSlug(%q) = %v, want %v", slug, got, want)
		}
	}
}

// §9.4: el fragmento del buscador dice bajo qué encabezado está la coincidencia, con el mismo
// id que tiene ese encabezado en la página, para saltar a él.
func TestSectionAt(t *testing.T) {
	md := "Intro con vpn arriba.\n\n## Instalación\n\nDescarga el cliente vpn del portal.\n\n## Conectarse\n\nAbre el cliente y elige vpn.\n"
	_, toc, err := Render(md, Options{})
	if err != nil || len(toc) != 2 {
		t.Fatalf("toc = %+v, %v", toc, err)
	}
	mark := func(s, word string) string {
		return strings.Replace(s, word, repo.HitStart+word+repo.HitEnd, 1)
	}
	for _, tc := range []struct {
		snippet string
		want    string // id del encabezado; "" = sin sección
	}{
		{"…" + mark("Descarga el cliente vpn del portal.", "vpn") + " …", toc[0].ID},
		{mark("Abre el cliente y elige vpn.", "vpn"), toc[1].ID},
		{mark("Intro con vpn arriba.", "vpn") + " …", ""}, // antes del primer encabezado
		// El fragmento empieza en una sección y la coincidencia cae en la siguiente.
		{"…del portal. " + mark("Abre el cliente", "Abre") + "…", toc[1].ID},
		{"sin coincidencia marcada", ""},
	} {
		got, ok := SectionAt(md, tc.snippet)
		if tc.want == "" && ok || tc.want != "" && (!ok || got.ID != tc.want) {
			t.Errorf("SectionAt(%q) = %+v %v, want %q", tc.snippet, got, ok, tc.want)
		}
	}
}

// §4.3: una línea cambiada marca solo lo que cambió dentro de ella.
func TestDiffWithinLine(t *testing.T) {
	got := Diff("a\nla vpn falla seguido\nc\n", "a\nla vpn conecta seguido\nc\n")
	if len(got) != 4 || got[1].Op != "-" || got[2].Op != "+" {
		t.Fatalf("Diff = %+v", got)
	}
	join := func(ps []DiffPart) (all, changed string) {
		for _, p := range ps {
			all += p.Text
			if p.Changed {
				changed += p.Text
			}
		}
		return
	}
	if all, changed := join(got[1].Parts); all != "la vpn falla seguido" || changed != "falla" {
		t.Errorf("deleted parts = %+v", got[1].Parts)
	}
	if all, changed := join(got[2].Parts); all != "la vpn conecta seguido" || changed != "conecta" {
		t.Errorf("inserted parts = %+v", got[2].Parts)
	}
	if got[0].Parts != nil || got[3].Parts != nil {
		t.Errorf("unchanged lines carry parts: %+v", got)
	}
	// Líneas sin nada en común: el resaltado sería toda la línea, así que no se marca nada.
	if d := Diff("uno\n", "dos\n"); d[0].Parts != nil || d[1].Parts != nil {
		t.Errorf("unrelated lines carry parts: %+v", d)
	}
	// Una línea agregada sin pareja no lleva partes.
	if d := Diff("a\n", "a\nnueva\n"); d[1].Parts != nil {
		t.Errorf("added line carries parts: %+v", d)
	}
}

func TestDiff(t *testing.T) {
	got := Diff("a\nb\nc\n", "a\nB\nc\n")
	want := []DiffLine{{Op: "=", Text: "a"}, {Op: "-", Text: "b"}, {Op: "+", Text: "B"}, {Op: "=", Text: "c"}}
	if len(got) != len(want) {
		t.Fatalf("Diff = %+v", got)
	}
	for i := range want {
		if got[i].Op != want[i].Op || got[i].Text != want[i].Text {
			t.Errorf("line %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
