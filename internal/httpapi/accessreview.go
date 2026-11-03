package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/athomo001/kudawiki/internal/accessreview"
	"github.com/athomo001/kudawiki/internal/repo"
)

// accessReviewService es la revisión periódica de accesos (§21.2.7).
type accessReviewService interface {
	Area(ctx context.Context, sc repo.Scope) (accessreview.Status, []repo.Member, error)
	ConfirmArea(ctx context.Context, sc repo.Scope, u repo.User) error
	Global(ctx context.Context) (accessreview.Status, []repo.AdminAccess, []accessreview.AreaStatus, error)
	ConfirmGlobal(ctx context.Context, u repo.User) error
}

func accessReviewJSON(st accessreview.Status) map[string]any {
	out := map[string]any{"due_at": st.DueAt.UTC().Format(time.RFC3339), "overdue": st.Overdue, "last": nil}
	if st.Last != nil {
		out["last"] = map[string]any{"at": st.Last.At.UTC().Format(time.RFC3339), "by": st.Last.ReviewerName}
	}
	return out
}

// areaAccessReview es la lista que confirma el admin del área: miembros, rol y de dónde viene.
func (a api) areaAccessReview(w http.ResponseWriter, r *http.Request) {
	st, ms, err := a.AccessReview.Area(r.Context(), scopeOf(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	people := make([]map[string]string, len(ms))
	for i, m := range ms {
		people[i] = map[string]string{"user_id": m.UserID, "email": m.Email, "full_name": m.FullName, "role": m.Role, "ldap_group": m.Group}
	}
	out := accessReviewJSON(st)
	out["members"] = people
	a.json(w, http.StatusOK, out)
}

func (a api) confirmAreaAccessReview(w http.ResponseWriter, r *http.Request) {
	if err := a.AccessReview.ConfirmArea(r.Context(), scopeOf(r), currentUser(r)); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// globalAccessReview es la del superadmin: quién administra qué, y cómo va cada área.
func (a api) globalAccessReview(w http.ResponseWriter, r *http.Request) {
	st, admins, areas, err := a.AccessReview.Global(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	people := make([]map[string]any, len(admins))
	for i, p := range admins {
		people[i] = map[string]any{"user_id": p.UserID, "email": p.Email, "full_name": p.FullName,
			"superadmin": p.Superadmin, "active": p.Active, "areas": append([]string{}, p.Areas...)}
	}
	rows := make([]map[string]any, len(areas))
	for i, ar := range areas {
		row := accessReviewJSON(ar.Status)
		row["slug"], row["name"] = ar.Slug, ar.Name
		rows[i] = row
	}
	out := accessReviewJSON(st)
	out["admins"], out["areas"] = people, rows
	a.json(w, http.StatusOK, out)
}

func (a api) confirmGlobalAccessReview(w http.ResponseWriter, r *http.Request) {
	if err := a.AccessReview.ConfirmGlobal(r.Context(), currentUser(r)); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
