// Package audit registra lo que se hace en la wiki (SPEC §11). Cada evento sella al anterior
// con SHA-256, así `kudawiki audit verify` detecta una fila cambiada, borrada o intercalada.
//
// Reglas de §11.2: se registra después de que la acción tuvo éxito, desde el servicio que
// atiende a la persona; lo que hace un job va sin actor y con meta.system; meta lleva
// identidad (ids, números de revisión), nunca contenido.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/repo"
)

// Action es una acción de la lista cerrada de §11.1. El campo no se exporta: fuera de este
// paquete no se puede inventar una acción, así una que no está en la lista no compila.
type Action struct{ name string }

func (a Action) String() string { return a.name }

var (
	AuthLogin        = Action{"auth.login"}
	AuthLoginFailed  = Action{"auth.login_failed"}
	AuthLockout      = Action{"auth.lockout"}
	AuthLogout       = Action{"auth.logout"}
	UserInvite       = Action{"user.invite"}
	UserInviteAccept = Action{"user.invite_accept"}
	WsCreate         = Action{"ws.create"}
	WsUpdate         = Action{"ws.update"}
	BackupRun        = Action{"backup.run"}
	WsMemberAdd      = Action{"ws.member_add"}
	WsMemberRemove   = Action{"ws.member_remove"}
	WsMemberRole     = Action{"ws.member_role_change"}
	PageCreate       = Action{"page.create"}
	PageUpdate       = Action{"page.update"}
	PageRevert       = Action{"page.revert"}
	PageMove         = Action{"page.move"}
	PageLevelChange  = Action{"page.level_change"}
	PageDelete       = Action{"page.delete"}
	PageRestore      = Action{"page.restore"}
	PagePurge        = Action{"page.purge"}
	PageRead         = Action{"page.read"}             // solo páginas N3 (§11.1)
	PathUnavailable  = Action{"page.path_unavailable"} // §19.35
	MediaUpload      = Action{"media.upload"}
	// Recuperación por consola (§13.6): las corre quien tiene shell en el servidor.
	CLIUnlock           = Action{"admin.cli_unlock"}
	CLISetPassword      = Action{"admin.cli_set_password"}
	CLICreateSuperadmin = Action{"admin.cli_create_superadmin"}
	CLIRevokeSessions   = Action{"admin.cli_revoke_sessions"}
	// Marca de la empresa (§15.1.5).
	SiteBrandingUpdate = Action{"site.branding_update"}
	SiteAssetUpload    = Action{"site.asset_upload"}
	SiteAssetDelete    = Action{"site.asset_delete"}
)

// All son todas las acciones; cada una tiene su traducción en la interfaz (lo exige un test).
var All = []Action{
	AuthLogin, AuthLoginFailed, AuthLockout, AuthLogout, UserInvite, UserInviteAccept,
	WsCreate, WsUpdate, BackupRun, WsMemberAdd, WsMemberRemove, WsMemberRole,
	PageCreate, PageUpdate, PageRevert, PageMove, PageLevelChange, PageDelete, PageRestore, PagePurge,
	PageRead, PathUnavailable, MediaUpload,
	CLIUnlock, CLISetPassword, CLICreateSuperadmin, CLIRevokeSessions,
	SiteBrandingUpdate, SiteAssetUpload, SiteAssetDelete,
}

// Actor es quien hace la acción, tal como está en ese momento.
type Actor struct{ ID, Email, Name, IP string }

type actorKey struct{}

// WithActor deja al actor en el contexto del request: los servicios registran sin tener que
// recibir la IP ni el nombre como parámetros.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

func actorFrom(ctx context.Context) Actor {
	a, _ := ctx.Value(actorKey{}).(Actor)
	return a
}

// Event es lo que se registra.
type Event struct {
	Action     Action
	WsID       string
	TargetType string // page, user, workspace, media
	TargetID   string
	Meta       map[string]any
	Actor      *Actor // quien lo hizo, cuando todavía no está en el contexto (el login)
	System     string // el job que lo hizo; entonces no hay actor
}

// TimeFormat tiene ancho fijo para que las fechas ordenen bien como texto.
const TimeFormat = "2006-01-02T15:04:05.000000Z"

type store interface {
	AppendAudit(ctx context.Context, row repo.AuditRow, seal func(prev string) string) error
}

type Recorder struct {
	store store
	now   func() time.Time
	log   *slog.Logger
}

func NewRecorder(s store, now func() time.Time, log *slog.Logger) *Recorder {
	return &Recorder{store: s, now: now, log: log}
}

// Record agrega el evento al final de la cadena. La acción ya ocurrió: si el registro falla no
// se deshace ni se responde un error a quien la hizo (reintentaría y la haría dos veces); queda
// en el log técnico para que se investigue.
func (r *Recorder) Record(ctx context.Context, e Event) {
	meta := sanitizeMeta(e.Meta)
	row := repo.AuditRow{At: r.now().UTC().Format(TimeFormat), WsID: e.WsID, Action: e.Action.name,
		TargetType: e.TargetType, TargetID: e.TargetID}
	snapshot := map[string]string{}
	if e.System != "" {
		meta["system"] = e.System
	} else {
		a := actorFrom(ctx)
		if e.Actor != nil {
			a = Actor{ID: e.Actor.ID, Email: e.Actor.Email, Name: e.Actor.Name, IP: a.IP}
		}
		row.ActorID = a.ID
		snapshot = map[string]string{"email": a.Email, "name": a.Name, "ip": a.IP}
	}
	m, _ := json.Marshal(meta) // las claves salen ordenadas: el mismo evento da los mismos bytes
	s, _ := json.Marshal(snapshot)
	row.Meta, row.Snapshot = string(m), string(s)
	// Si quien pidió la acción se desconecta, el registro igual se escribe.
	if err := r.store.AppendAudit(context.WithoutCancel(ctx), row, func(prev string) string { return Seal(prev, row) }); err != nil {
		r.log.Error("audit write failed", "action", e.Action.name, "err", err)
	}
}

// Genesis es el prev_hash del primer evento.
var Genesis = strings.Repeat("0", 64)

// Seal es hash = SHA-256(prev_hash || fila) (§11.1). La copia del actor queda fuera, para poder
// seudonimizarla sin romper la cadena (§11.2, §13.10).
func Seal(prev string, row repo.AuditRow) string {
	body, _ := json.Marshal([]string{row.At, row.ActorID, row.WsID, row.Action, row.TargetType, row.TargetID, row.Meta})
	sum := sha256.Sum256(append([]byte(prev), body...))
	return hex.EncodeToString(sum[:])
}

// secretWords marcan las claves de meta que se redactan por si alguien pasa un secreto (§11.2).
var secretWords = []string{"password", "secret", "token", "hash", "api_key", "totp"}

func sanitizeMeta(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		for _, w := range secretWords {
			if strings.Contains(strings.ToLower(k), w) {
				v = "[redacted]"
				break
			}
		}
		out[k] = v
	}
	return out
}
