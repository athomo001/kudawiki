package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/go-chi/chi/v5"
)

// notificationRoutes son la bandeja y las preferencias de cada persona (SPEC §12.1, §14). Todo
// filtra por quien pregunta: no hay forma de pedir los avisos de otro.
func (a api) notificationRoutes(r chi.Router) {
	r.Get("/me/notifications", a.notifications)
	r.Post("/me/notifications/read", a.readNotifications)
	r.Get("/me/notification-prefs", a.notificationPrefs)
	r.Put("/me/notification-prefs", a.setNotificationPrefs)
}

func (a api) notifications(w http.ResponseWriter, r *http.Request) {
	list, unread, err := a.Notify.List(r.Context(), currentUser(r).ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i, n := range list {
		var p map[string]any
		_ = json.Unmarshal([]byte(n.Payload), &p)
		out[i] = map[string]any{"id": n.ID, "kind": n.Kind, "area": map[string]string{"slug": n.AreaSlug, "name": n.AreaName},
			"page": n.PageSlug, "proposal": n.ProposalID, "title": p["title"], "actor": p["actor"], "days": p["days"],
			"count": n.Count, "read": n.Read, "updated_at": n.UpdatedAt}
	}
	a.json(w, http.StatusOK, map[string]any{"items": out, "unread": unread})
}

// readNotifications marca como leídos los avisos de ids, o todos si no viene ninguno.
func (a api) readNotifications(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if len(in.IDs) > 100 {
		a.error(w, r, http.StatusBadRequest, "invalid_request", nil)
		return
	}
	if err := a.Notify.Read(r.Context(), currentUser(r).ID, in.IDs); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) notificationPrefs(w http.ResponseWriter, r *http.Request) {
	p, err := a.Notify.Prefs(r.Context(), currentUser(r).ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// email_enabled: sin correo configurado, la interfaz avisa que solo hay bandeja (§12).
	enabled, err := a.Mail.Enabled(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, map[string]any{"auto_watch": p.AutoWatch, "email": p.Email, "email_enabled": enabled})
}

func (a api) setNotificationPrefs(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AutoWatch bool              `json:"auto_watch"`
		Email     map[string]string `json:"email"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if err := a.Notify.SetPrefs(r.Context(), currentUser(r).ID, repo.NotificationPrefs{AutoWatch: in.AutoWatch, Email: in.Email}); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// watch sigue la página ({"subtree": true}: con sus subpáginas). Cualquiera que la lee puede
// seguirla; la que no ve es el 404 uniforme.
func (a api) watch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Subtree bool `json:"subtree"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	mode := "page"
	if in.Subtree {
		mode = "subtree"
	}
	a.setWatch(w, r, mode)
}

func (a api) unwatch(w http.ResponseWriter, r *http.Request) { a.setWatch(w, r, "") }

func (a api) setWatch(w http.ResponseWriter, r *http.Request, mode string) {
	if err := a.Notify.Watch(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "*"), mode); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// proposalsSeen: el admin abrió las pendientes del área, así que sus avisos de propuestas por
// revisar quedan leídos y ya no salen por correo.
func (a api) proposalsSeen(w http.ResponseWriter, r *http.Request) {
	if err := a.Notify.PendingOpened(r.Context(), scopeOf(r), currentUser(r).ID); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// unsubscribe es la baja en un clic (RFC 8058). Va fuera de la sesión y de la protección CSRF:
// el POST lo hace el servidor de correo, sin cookie ni Origin, y lo autoriza la firma del
// enlace. Abrir el enlace (GET) solo muestra la confirmación, que hace este mismo POST.
func (a api) unsubscribe(w http.ResponseWriter, r *http.Request) {
	if err := a.Notify.Unsubscribe(r.Context(), r.URL.Query().Get("t")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
