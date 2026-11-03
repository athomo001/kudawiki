package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// lifecycleService es el ciclo de vida de un área (§3.5) y su slug (B17).
type lifecycleService interface {
	SetStatus(ctx context.Context, id, status string) error
	Rename(ctx context.Context, id, slug string) error
	RequestDelete(ctx context.Context, actorID, id, confirm string) (time.Time, error)
	CancelDelete(ctx context.Context, id string) error
}

func (a api) setWorkspaceStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status string `json:"status"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if err := a.Lifecycle.SetStatus(r.Context(), chi.URLParam(r, "id"), in.Status); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) setWorkspaceSlug(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Slug string `json:"slug"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if err := a.Lifecycle.Rename(r.Context(), chi.URLParam(r, "id"), in.Slug); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) deleteWorkspace(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Confirm string `json:"confirm"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	at, err := a.Lifecycle.RequestDelete(r.Context(), currentUser(r).ID, chi.URLParam(r, "id"), in.Confirm)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, map[string]any{"delete_at": at.UTC()})
}

func (a api) cancelWorkspaceDelete(w http.ResponseWriter, r *http.Request) {
	if err := a.Lifecycle.CancelDelete(r.Context(), chi.URLParam(r, "id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
