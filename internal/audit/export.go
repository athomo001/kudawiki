package audit

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"strconv"

	"github.com/athomo001/kudawiki/internal/repo"
)

// Exported es un evento tal como sale de la wiki: en la exportación JSONL/CSV, en el paquete de
// evidencia y en la copia a syslog (§11, §21.2.7). meta_json va tal como se selló, como texto:
// así quien lo recibe puede recalcular el hash de cada fila (ver HashFormula).
type Exported struct {
	ID         int64             `json:"id"`
	At         string            `json:"at"`
	Action     string            `json:"action"`
	ActorID    string            `json:"actor_id,omitempty"`
	Actor      map[string]string `json:"actor,omitempty"`
	WsID       string            `json:"workspace_id,omitempty"`
	Workspace  string            `json:"workspace,omitempty"`
	Page       string            `json:"page,omitempty"`
	TargetType string            `json:"target_type,omitempty"`
	TargetID   string            `json:"target_id,omitempty"`
	MetaJSON   string            `json:"meta_json"`
	PrevHash   string            `json:"prev_hash"`
	Hash       string            `json:"hash"`
}

// HashFormula explica, para quien recibe la exportación, cómo se comprueba cada fila.
const HashFormula = `hash = hex(SHA-256(prev_hash || JSON([at, actor_id, workspace_id, action, target_type, target_id, meta_json])))` +
	`, with the array as compact JSON of strings ("" when empty) and prev_hash of the first event = 64 zeros`

// Export arma el evento exportado. La IP es un dato personal (§13.10): solo va si withIP; un
// evento seudonimizado tras la baja lleva solo el seudónimo.
func Export(e repo.AuditEntry, withIP bool) Exported {
	out := Exported{ID: e.ID, At: e.At, Action: e.Action, ActorID: e.ActorID, WsID: e.WsID, Workspace: e.WsSlug, Page: e.PageSlug,
		TargetType: e.TargetType, TargetID: e.TargetID, MetaJSON: e.Meta, PrevHash: e.PrevHash, Hash: e.Hash}
	var snap map[string]string
	json.Unmarshal([]byte(e.Snapshot), &snap)
	switch {
	case snap["pseudonym"] != "":
		out.Actor = map[string]string{"pseudonym": snap["pseudonym"]}
	case e.ActorID != "":
		out.Actor = map[string]string{"email": snap["email"], "name": snap["name"]}
		if withIP && snap["ip"] != "" {
			out.Actor["ip"] = snap["ip"]
		}
	}
	return out
}

// EventWriter escribe eventos exportados en un formato.
type EventWriter interface {
	Write(Exported) error
	Close() error
}

// NewJSONL escribe un evento por línea.
func NewJSONL(w io.Writer) EventWriter {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return jsonlWriter{enc}
}

type jsonlWriter struct{ enc *json.Encoder }

func (j jsonlWriter) Write(e Exported) error { return j.enc.Encode(e) }
func (j jsonlWriter) Close() error           { return nil }

// NewCSV escribe una fila por evento, con encabezado.
func NewCSV(w io.Writer) EventWriter {
	c := &csvWriter{w: csv.NewWriter(w)}
	return c
}

type csvWriter struct {
	w      *csv.Writer
	headed bool
}

var csvHeader = []string{"id", "at", "action", "actor_id", "actor_email", "actor_name", "actor_ip", "actor_pseudonym",
	"workspace_id", "workspace", "page", "target_type", "target_id", "meta_json", "prev_hash", "hash"}

func (c *csvWriter) Write(e Exported) error {
	if !c.headed {
		c.headed = true
		if err := c.w.Write(csvHeader); err != nil {
			return err
		}
	}
	return c.w.Write([]string{strconv.FormatInt(e.ID, 10), e.At, e.Action, e.ActorID, csvSafe(e.Actor["email"]), csvSafe(e.Actor["name"]),
		e.Actor["ip"], e.Actor["pseudonym"], e.WsID, e.Workspace, csvSafe(e.Page), e.TargetType, e.TargetID, e.MetaJSON, e.PrevHash, e.Hash})
}

func (c *csvWriter) Close() error {
	c.w.Flush()
	return c.w.Error()
}

// csvSafe evita que una planilla ejecute como fórmula un nombre que empieza con = + - @ (CSV
// injection): el nombre y la ruta los escribe una persona.
func csvSafe(s string) string {
	if s != "" && (s[0] == '=' || s[0] == '+' || s[0] == '-' || s[0] == '@' || s[0] == '\t' || s[0] == '\r') {
		return "'" + s
	}
	return s
}
