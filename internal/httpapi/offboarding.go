package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/athomo001/kudawiki/internal/offboarding"
	"github.com/go-chi/chi/v5"
)

// offboardingService es la baja de usuarios y el superadmin desde el panel (§8.4, B15).
type offboardingService interface {
	Plan(ctx context.Context, actorID, userID string) (offboarding.Plan, error)
	Start(ctx context.Context, actorID, userID string, receivers map[string]string, now bool) (offboarding.Outcome, error)
	Reactivate(ctx context.Context, actorID, userID string) error
	SetSuperadmin(ctx context.Context, actorID, userID string, on bool) error
}

// offboardingRoutes cuelgan de /api/admin: solo el superadmin, y el resto ve el 404 uniforme.
// Iniciar la baja desactiva la cuenta y puede completarla en el acto: pide reautenticarse (§8.2).
func (a api) offboardingRoutes(r chi.Router) {
	r.Get("/users/{id}/offboarding", a.offboardingPlan)
	r.With(a.requireReauth).Post("/users/{id}/offboarding", a.startOffboarding)
}

func (a api) offboardingPlan(w http.ResponseWriter, r *http.Request) {
	p, err := a.Offboarding.Plan(r.Context(), currentUser(r).ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	areas := make([]map[string]any, len(p.Areas))
	for i, x := range p.Areas {
		admins := make([]map[string]string, len(x.Admins))
		for j, ad := range x.Admins {
			admins[j] = map[string]string{"id": ad.ID, "full_name": ad.FullName}
		}
		areas[i] = map[string]any{"id": x.WsID, "slug": x.Slug, "name": x.Name, "role": x.Role, "pages": x.Pages,
			"quizzes": x.Quizzes, "read_requests": x.ReadRequests, "admins": admins, "receiver": x.Receiver,
			"to_you": x.ToActor, "needs_admin": x.NeedsAdmin}
	}
	out := map[string]any{"areas": areas, "personal_pages": p.PersonalPages, "personal_media": p.PersonalMedia,
		"drafts": p.Drafts, "proposals": p.Proposals, "grace_days": int(p.Grace / (24 * time.Hour))}
	if p.Pending != nil {
		out["pending"] = map[string]any{"until": p.Pending.Until, "receivers": p.Pending.Receivers}
	}
	a.json(w, http.StatusOK, out)
}

// startOffboarding es POST /admin/users/{id}/offboarding con {receivers, now}: receivers es
// {workspace_id: admin} y lo que falta toma el admin más antiguo (§8.4).
func (a api) startOffboarding(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Receivers map[string]string `json:"receivers"`
		Now       bool              `json:"now"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	o, err := a.Offboarding.Start(r.Context(), currentUser(r).ID, chi.URLParam(r, "id"), in.Receivers, in.Now)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if !o.Completed {
		a.json(w, http.StatusOK, map[string]any{"status": "pending", "until": o.Until})
		return
	}
	pages, quizzes, reads := 0, 0, 0
	for _, t := range o.Result.Transfers {
		pages, quizzes, reads = pages+len(t.PageIDs), quizzes+t.Quizzes, reads+t.ReadRequests
	}
	a.json(w, http.StatusOK, map[string]any{"status": "completed", "pages": pages, "quizzes": quizzes, "read_requests": reads,
		"personal_pages": o.Result.PersonalPages, "drafts": o.Result.Drafts, "proposals": o.Result.Proposals})
}
