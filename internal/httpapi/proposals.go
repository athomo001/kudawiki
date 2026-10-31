package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/proposals"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/go-chi/chi/v5"
)

// proposalRoutes son las propuestas (SPEC §5, §14.3). Quién ve y quién cambia cada una lo decide
// proposals.Service: aquí solo el rol mínimo de cada ruta.
func (a api) proposalRoutes(r chi.Router) {
	r.With(a.requireAuthor).Get("/approval/*", a.approval)
	r.With(a.requireAuthor).Post("/render", a.preview)
	// En el espacio personal no hay a quién proponerle nada (§7): todo se publica directo.
	r.Group(func(r chi.Router) {
		r.Use(a.areaOnly)
		r.Get("/proposals", a.listProposals)
		r.Get("/proposals/{id}", a.getProposal)
		r.With(a.requireRank(repo.RoleRank("admin"))).Post("/proposals/seen", a.proposalsSeen)
		r.Patch("/proposals/{id}", a.updateProposal)
		r.Post("/proposals/{id}/submit", a.resubmitProposal)
		r.Post("/proposals/{id}/withdraw", a.withdrawProposal)
		r.With(a.requireAuthor).Post("/proposals", a.createProposal)
	})
	r.Group(func(r chi.Router) {
		r.Use(a.areaOnly, a.requireRank(repo.RoleRank("admin")))
		r.Post("/proposals/{id}/approve", a.approveProposal)
		r.Post("/proposals/{id}/reject", a.rejectProposal)
		r.Get("/approval-overrides", a.approvalOverrides)
		r.Put("/approval-overrides/*", a.setApprovalOverride)
		r.Delete("/approval-overrides/*", a.deleteApprovalOverride)
	})
}

// requireAuthor deja pasar a quien puede escribir en el área: editores y admins, y los lectores
// si el área les permite proponer (§19.5). Para los demás, el 404 uniforme.
func (a api) requireAuthor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok, err := a.mayWrite(r.Context(), scopeOf(r))
		if err != nil {
			a.fail(w, r, err)
			return
		}
		if !ok {
			a.notFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a api) mayWrite(ctx context.Context, sc repo.Scope) (bool, error) {
	if sc.MaxRank >= repo.RoleRank("editor") {
		return true, nil
	}
	ws, err := a.Read.WorkspaceByID(ctx, sc.WsID)
	return err == nil && ws.ViewersCanPropose, err
}

// draftInput es lo que el editor manda al proponer o al corregir una propuesta.
type draftInput struct {
	Slug          string `json:"slug"`
	Title         string `json:"title"`
	Markdown      string `json:"markdown"`
	Lang          string `json:"lang"`
	BaseRevision  int    `json:"base_revision"` // 0 = página nueva
	ChangeSummary string `json:"change_summary"`
}

func (d draftInput) draft() proposals.Draft {
	return proposals.Draft{Slug: strings.TrimSpace(d.Slug), Title: d.Title, Markdown: d.Markdown, Lang: d.Lang, Summary: d.ChangeSummary, Base: d.BaseRevision}
}

func (a api) createProposal(w http.ResponseWriter, r *http.Request) {
	var in draftInput
	if !a.decode(w, r, &in) {
		return
	}
	id, err := a.Proposals.Create(r.Context(), scopeOf(r), currentUser(r).ID, in.draft())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.forgetDraft(r, in.draft().Slug)
	a.json(w, http.StatusCreated, map[string]string{"id": id})
}

// proposalJSON es una propuesta en los listados (sin el contenido).
func proposalJSON(p repo.Proposal) map[string]any {
	var reviewed any // null mientras no se resuelve
	if !p.ReviewedAt.IsZero() {
		reviewed = p.ReviewedAt
	}
	return map[string]any{
		"id": p.ID, "slug": p.Slug, "title": p.Title, "status": p.Status, "new_page": p.PageID == "" || (p.Status == "merged" && p.Base == 0),
		"base_revision": p.Base, "current_revision": p.Current, "level": p.MinRank, "summary": p.Summary,
		"author": p.Author, "author_id": p.AuthorID, "reviewer": p.Reviewer, "comment": p.Comment, "merged_revision": p.Merged,
		"created_at": p.CreatedAt, "reviewed_at": reviewed,
	}
}

func (a api) listProposals(w http.ResponseWriter, r *http.Request) {
	view := r.URL.Query().Get("view")
	if view == "" {
		view = "pending"
	}
	list, err := a.Proposals.List(r.Context(), scopeOf(r), currentUser(r).ID, view)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i, p := range list {
		out[i] = proposalJSON(p)
		// overdue: ya le tocó el recordatorio al admin (§5.3); la bandeja lo marca.
		out[i]["overdue"] = p.Status == "pending" && time.Since(p.CreatedAt) > a.RemindAfter
	}
	a.json(w, http.StatusOK, out)
}

// getProposal es el detalle para revisar (§5.2): los cambios, si la página avanzó, si se mezcla
// sola, y la vista previa ya renderizada con el mismo render que el lector.
func (a api) getProposal(w http.ResponseWriter, r *http.Request) {
	d, err := a.Proposals.Review(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.Notify.ProposalOpened(r.Context(), currentUser(r).ID, d.ID); err != nil {
		a.Log.Error("mark notifications read", "err", err)
	}
	html, _, err := wiki.Render(d.Result, a.renderOptions(r, d.Slug, d.Result))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := proposalJSON(d.Proposal)
	out["markdown"], out["lang"], out["html"] = d.Markdown, d.Lang, html
	out["changes"], out["page_changed"], out["conflict"] = d.Changes, d.PageChanged, d.Conflict
	out["against_current"], out["page_changes"] = d.AgainstCurrent, d.PageChanges
	out["result"], out["result_title"] = d.Result, d.ResultTitle
	a.json(w, http.StatusOK, out)
}

func (a api) updateProposal(w http.ResponseWriter, r *http.Request) {
	var in draftInput
	if !a.decode(w, r, &in) {
		return
	}
	if err := a.Proposals.Update(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id"), in.draft()); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) resubmitProposal(w http.ResponseWriter, r *http.Request) {
	a.noContent(w, r, a.Proposals.Resubmit(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id")))
}

func (a api) withdrawProposal(w http.ResponseWriter, r *http.Request) {
	a.noContent(w, r, a.Proposals.Withdraw(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id")))
}

// approveProposal publica. Con title y markdown es "aprobar con modificaciones" (§5.2).
func (a api) approveProposal(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Comment string      `json:"comment"`
		Edit    *draftInput `json:"edit"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	var edit *proposals.Draft
	if in.Edit != nil {
		d := in.Edit.draft()
		edit = &d
	}
	rev, err := a.Proposals.Approve(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id"), in.Comment, edit)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, map[string]int{"revision": rev})
}

func (a api) rejectProposal(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Comment string `json:"comment"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	a.noContent(w, r, a.Proposals.Reject(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id"), in.Comment))
}

// approval le dice al editor si en esa ruta quien escribe publica directo o envía a revisión (§5.1).
func (a api) approval(w http.ResponseWriter, r *http.Request) {
	must, err := a.Wiki.MustPropose(r.Context(), scopeOf(r), chi.URLParam(r, "*"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, map[string]bool{"must_propose": must})
}

func (a api) approvalOverrides(w http.ResponseWriter, r *http.Request) {
	list, err := a.Wiki.ApprovalOverrides(r.Context(), scopeOf(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]string, len(list))
	for i, o := range list {
		out[i] = map[string]string{"prefix": o.Prefix, "approval_mode": o.Mode}
	}
	a.json(w, http.StatusOK, out)
}

func (a api) setApprovalOverride(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ApprovalMode string `json:"approval_mode"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	a.noContent(w, r, a.Wiki.SetApprovalOverride(r.Context(), scopeOf(r), chi.URLParam(r, "*"), in.ApprovalMode))
}

func (a api) deleteApprovalOverride(w http.ResponseWriter, r *http.Request) {
	a.noContent(w, r, a.Wiki.DeleteApprovalOverride(r.Context(), scopeOf(r), chi.URLParam(r, "*")))
}

// noContent responde 204, o el error.
func (a api) noContent(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
