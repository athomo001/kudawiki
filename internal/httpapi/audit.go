package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/athomo001/kudawiki/internal/repo"
)

const auditPage = 100

// auditFilter lee los filtros de la pantalla de auditoría (SPEC §14.2). ?before= pagina hacia atrás.
func auditFilter(r *http.Request) repo.AuditFilter {
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	return repo.AuditFilter{WsSlug: q.Get("ws"), ActorEmail: q.Get("actor"), Action: q.Get("action"),
		From: q.Get("from"), To: q.Get("to"), Before: before, Limit: auditPage + 1}
}

// auditJSON arma la respuesta. La IP solo la ve el superadmin: es un dato personal (§13.10).
func (a api) auditJSON(w http.ResponseWriter, r *http.Request, es []repo.AuditEntry, err error, withIP bool) {
	if err != nil {
		a.fail(w, r, err)
		return
	}
	more := len(es) > auditPage
	if more {
		es = es[:auditPage]
	}
	out := make([]map[string]any, len(es))
	for i, e := range es {
		var meta map[string]any
		var actor map[string]string
		json.Unmarshal([]byte(e.Meta), &meta)
		json.Unmarshal([]byte(e.Snapshot), &actor)
		ev := map[string]any{"id": e.ID, "at": e.At, "action": e.Action, "target_type": e.TargetType,
			"target_id": e.TargetID, "meta": meta}
		if e.ActorID != "" {
			ev["actor"] = map[string]string{"name": actor["name"], "email": actor["email"]}
			if withIP {
				ev["ip"] = actor["ip"]
			}
		}
		if e.WsSlug != "" {
			ev["workspace"] = e.WsSlug
		}
		if e.PageSlug != "" {
			ev["page"] = e.PageSlug
		}
		out[i] = ev
	}
	a.json(w, http.StatusOK, map[string]any{"events": out, "more": more})
}

// audit es la vista del superadmin, de todas las áreas, sin rutas ni títulos de páginas.
func (a api) audit(w http.ResponseWriter, r *http.Request) {
	es, err := a.Read.AuditEvents(r.Context(), auditFilter(r))
	a.auditJSON(w, r, es, err, true)
}

// areaAudit es la del admin del área: solo su área; el filtro lo aplica repo (§11.2).
func (a api) areaAudit(w http.ResponseWriter, r *http.Request) {
	f := auditFilter(r)
	f.WsSlug = ""
	es, err := a.Read.AreaAudit(r.Context(), scopeOf(r), f)
	a.auditJSON(w, r, es, err, false)
}
