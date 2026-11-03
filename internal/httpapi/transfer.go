package httpapi

import (
	"context"
	"net/http"

	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/transfer"
)

// transferService es el traslado de páginas entre áreas (§4.13).
type transferService interface {
	Preview(ctx context.Context, req transfer.Request) (transfer.Preview, error)
	Transfer(ctx context.Context, actor repo.User, req transfer.Request) (transfer.Preview, error)
}

type transferIn struct {
	From  string `json:"from"`  // id del área de origen
	Slug  string `json:"slug"`  // la página (y su subárbol)
	Dest  string `json:"dest"`  // id del área de destino
	To    string `json:"to"`    // la ruta en el destino
	Level int    `json:"level"` // 0: cada una conserva el suyo
}

func (in transferIn) request() transfer.Request {
	return transfer.Request{From: in.From, Slug: in.Slug, Dest: in.Dest, To: in.To, Level: in.Level}
}

// La vista previa muestra rutas, no títulos ni contenido: el superadmin no es miembro de las
// áreas, y la ruta es lo que ya escribió para pedir el traslado (§3.3, §4.13).
func previewJSON(p transfer.Preview) map[string]any {
	plans := func(ps []transfer.Plan) []map[string]any {
		out := make([]map[string]any, len(ps))
		for i, x := range ps {
			out[i] = map[string]any{"slug": x.Slug, "new_slug": x.NewSlug, "level": x.Level, "new_level": x.NewLevel}
		}
		return out
	}
	links := make([]map[string]string, len(p.Impact.BrokenLinks))
	for i, l := range p.Impact.BrokenLinks {
		links[i] = map[string]string{"from": l.FromSlug, "to": l.ToSlug}
	}
	nonNil := func(s []string) []string { return append([]string{}, s...) }
	return map[string]any{"pages": plans(p.Pages), "sealed": plans(p.Sealed), "taken": nonNil(p.Impact.Taken), "broken_links": links,
		"proposals": p.Impact.Proposals, "shares": p.Impact.Shares, "reads": p.Impact.Reads, "lose": nonNil(p.Lose), "gain": nonNil(p.Gain)}
}

func (a api) previewTransfer(w http.ResponseWriter, r *http.Request) {
	var in transferIn
	if !a.decode(w, r, &in) {
		return
	}
	p, err := a.Transfer.Preview(r.Context(), in.request())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, previewJSON(p))
}

func (a api) transferPages(w http.ResponseWriter, r *http.Request) {
	var in transferIn
	if !a.decode(w, r, &in) {
		return
	}
	p, err := a.Transfer.Transfer(r.Context(), currentUser(r), in.request())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, previewJSON(p))
}
