package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
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
		switch {
		case actor["pseudonym"] != "":
			// Seudonimizada tras la baja (§13.10): ni nombre ni correo ni IP.
			ev["actor"] = map[string]string{"pseudonym": actor["pseudonym"]}
		case e.ActorID != "":
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
	es, err := a.Read.AreaAudit(r.Context(), scopeOf(r), currentUser(r).ID, f)
	a.auditJSON(w, r, es, err, false)
}

// exportAudit baja la auditoría del superadmin en JSONL o CSV (§11), con los mismos filtros de la
// pantalla y todas las páginas. Con la IP, como su vista.
func (a api) exportAudit(w http.ResponseWriter, r *http.Request) {
	a.writeAuditExport(w, r, "", true, func(f repo.AuditFilter) ([]repo.AuditEntry, error) {
		return a.Read.AuditEvents(r.Context(), f)
	})
}

// exportAreaAudit es la del admin del área: el mismo filtro de repo que su vista (§11.2), sin IP.
func (a api) exportAreaAudit(w http.ResponseWriter, r *http.Request) {
	sc, viewer := scopeOf(r), currentUser(r).ID
	a.writeAuditExport(w, r, sc.WsID, false, func(f repo.AuditFilter) ([]repo.AuditEntry, error) {
		f.WsSlug = ""
		return a.Read.AreaAudit(r.Context(), sc, viewer, f)
	})
}

const exportBatch = 1000

func (a api) writeAuditExport(w http.ResponseWriter, r *http.Request, wsID string, withIP bool, fetch func(repo.AuditFilter) ([]repo.AuditEntry, error)) {
	format := r.URL.Query().Get("format")
	if format != "jsonl" && format != "csv" {
		a.error(w, r, http.StatusBadRequest, "invalid_request", nil)
		return
	}
	f := auditFilter(r)
	f.Before, f.Limit = 0, exportBatch
	first, err := fetch(f)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	ctype := "application/x-ndjson"
	if format == "csv" {
		ctype = "text/csv; charset=utf-8"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", `attachment; filename="kudawiki-audit-`+time.Now().UTC().Format("20060102-150405")+"."+format+`"`)
	var out audit.EventWriter
	if format == "csv" {
		out = audit.NewCSV(w)
	} else {
		out = audit.NewJSONL(w)
	}
	n := 0
	for batch := first; len(batch) > 0; {
		for _, e := range batch {
			if err := out.Write(audit.Export(e, withIP)); err != nil {
				return // la conexión se cortó: ya no hay a quién responderle
			}
			n++
		}
		if len(batch) < exportBatch {
			break
		}
		f.Before = batch[len(batch)-1].ID
		if batch, err = fetch(f); err != nil {
			a.Log.Error("audit export failed midway", "err", err)
			return
		}
	}
	if out.Close() != nil {
		return
	}
	q := r.URL.Query()
	a.Audit.Record(r.Context(), audit.Event{Action: audit.AuditExport, WsID: wsID, TargetType: "audit",
		Meta: map[string]any{"format": format, "events": n, "from": q.Get("from"), "to": q.Get("to"),
			"filtered": q.Get("actor") != "" || q.Get("action") != "" || q.Get("ws") != ""}})
}
