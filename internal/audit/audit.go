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
	AuthLogin       = Action{"auth.login"}
	AuthLoginFailed = Action{"auth.login_failed"}
	AuthLockout     = Action{"auth.lockout"}
	AuthLogout      = Action{"auth.logout"}
	// Perfil de la persona misma (§8.5.2, §14.1).
	AuthPasswordChange = Action{"auth.password_change"}
	UserProfileUpdate  = Action{"user.profile_update"}
	UserInvite         = Action{"user.invite"}
	UserInviteAccept   = Action{"user.invite_accept"}
	WsCreate           = Action{"ws.create"}
	WsUpdate           = Action{"ws.update"}
	BackupRun          = Action{"backup.run"}
	// Exportar la auditoría (JSONL/CSV desde el panel, paquete de evidencia desde la consola, §11, §21.2.7).
	AuditExport = Action{"audit.export"}
	// El respaldo fuera del servidor y su verificación (§13.3, §21.2.4); backup.failed es
	// cualquiera de los pasos que falló, con meta.step.
	BackupUpload = Action{"backup.upload"}
	BackupVerify = Action{"backup.verify"}
	BackupFailed = Action{"backup.failed"}
	// F8-B: la configuración de los respaldos desde el panel y "Borrar todo".
	BackupSettingsUpdate = Action{"backup.settings_update"}
	SiteReset            = Action{"site.reset"}
	BackupDelete         = Action{"backup.delete"}   // F8-B.6: borrar un respaldo desde el panel
	BackupDownload       = Action{"backup.download"} // F8-B.7: bajarlo desde el panel, sin cifrar
	// La revisión periódica de accesos (§21.2.7): quién confirmó la lista y el aviso al vencer.
	AccessReviewConfirm = Action{"access_review.confirm"}
	AccessReviewDue     = Action{"access_review.due"}
	// Ciclo de vida de un área (§3.5) y su slug (B17).
	WsArchive       = Action{"ws.archive"}
	WsUnarchive     = Action{"ws.unarchive"}
	WsDeactivate    = Action{"ws.deactivate"}
	WsReactivate    = Action{"ws.reactivate"}
	WsDeleteRequest = Action{"ws.delete_request"}
	WsDeleteCancel  = Action{"ws.delete_cancel"}
	WsPurge         = Action{"ws.purge"}
	WsSlugChange    = Action{"ws.slug_change"}
	// Traslado entre áreas (§4.13): uno en cada área, unidos por meta.transfer.
	PageTransferOut = Action{"page.transfer_out"}
	PageTransferIn  = Action{"page.transfer_in"}
	// Exportar e importar un área como Markdown (§13.8).
	WsExport = Action{"ws.export"}
	WsImport = Action{"ws.import"}
	// Datos personales (§13.10): la purga por retención (cuántos, no de quién) y la exportación
	// de lo que la wiki tiene de una persona.
	PrivacyRetention = Action{"privacy.retention"}
	PrivacyExport    = Action{"privacy.export"}
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
	MediaDelete      = Action{"media.delete"}
	RevisionPurge    = Action{"revision.purge"} // la retención del historial del área (§4.3)
	// Anuncios del inicio del área (§4.17).
	AnnouncementCreate = Action{"announcement.create"}
	AnnouncementDelete = Action{"announcement.delete"}
	// Plantillas (§4.9): del área o globales (sin área).
	TemplateCreate = Action{"template.create"}
	TemplateUpdate = Action{"template.update"}
	TemplateDelete = Action{"template.delete"}
	// Vigencia (§4.8): marcar como vigente, y cambiar el dueño o el intervalo.
	PageVerify       = Action{"page.verify"}
	PageReviewUpdate = Action{"page.review_update"}
	// Espacio personal (§7): publicar una página en un área, y el superadmin abriendo uno ajeno.
	PagePublish       = Action{"page.publish"}
	PersonalAuditOpen = Action{"personal.audit_open"}
	// Acuse de lectura (§4.14).
	ReadRequestCreate = Action{"read_request.create"}
	ReadRequestClose  = Action{"read_request.close"}
	ReadAck           = Action{"read.ack"}
	// Cuestionarios (§4.16).
	QuizCreate             = Action{"quiz.create"}
	QuizUpdate             = Action{"quiz.update"}
	QuizPublish            = Action{"quiz.publish"}
	QuizArchive            = Action{"quiz.archive"}
	QuizDelete             = Action{"quiz.delete"}
	QuizAttemptStart       = Action{"quiz.attempt_start"}
	QuizAttemptSubmit      = Action{"quiz.attempt_submit"}
	QuizAttemptTimeout     = Action{"quiz.attempt_timeout"}
	QuizExtraAttemptsGrant = Action{"quiz.extra_attempts_grant"}
	QuizResultsExport      = Action{"quiz.results_export"}
	// Recuperación por consola (§13.6): las corre quien tiene shell en el servidor.
	CLIUnlock           = Action{"admin.cli_unlock"}
	CLISetPassword      = Action{"admin.cli_set_password"}
	CLICreateSuperadmin = Action{"admin.cli_create_superadmin"}
	CLIRevokeSessions   = Action{"admin.cli_revoke_sessions"}
	// Marca de la empresa (§15.1.5).
	SiteBrandingUpdate = Action{"site.branding_update"}
	SiteAssetUpload    = Action{"site.asset_upload"}
	SiteAssetDelete    = Action{"site.asset_delete"}
	// Correo (§12).
	SiteSMTPUpdate = Action{"site.smtp_update"}
	SMTPTestSend   = Action{"smtp.test_send"}
	// Directorio LDAP / AD (§8.6). La configuración guarda qué claves cambiaron, nunca sus valores.
	LDAPConfigUpdate = Action{"ldap.config_update"}
	LDAPTest         = Action{"ldap.test"}
	// Reglas grupo → (área, rol) del directorio (§8.3.6).
	LDAPMappingCreate = Action{"ldap.mapping_create"}
	LDAPMappingUpdate = Action{"ldap.mapping_update"}
	LDAPMappingDelete = Action{"ldap.mapping_delete"}
	// Cuentas y membresías del directorio (§8.3.2, §8.6.3) y su sincronización (§8.3.3, §8.3.6).
	// Lo que hace la sincronización periódica lleva meta.system = "ldap".
	LDAPAccountLink       = Action{"ldap.account_link"}
	LDAPMembershipDetach  = Action{"ldap.membership_detach"}
	LDAPSyncStart         = Action{"ldap.sync_start"}
	LDAPSyncDone          = Action{"ldap.sync_done"}
	LDAPSyncAborted       = Action{"ldap.sync_aborted"}
	UserDeactivatedByLDAP = Action{"user.deactivated_by_ldap"}
	// Recuperación de contraseña (§8.5).
	AuthPasswordResetRequest  = Action{"auth.password_reset_request"}
	AuthPasswordResetComplete = Action{"auth.password_reset_complete"}
	// Segundo factor (§8.1). El fallo se registra como el del login: el primero de cada ventana
	// y el bloqueo, no cada intento.
	AuthTOTPEnable       = Action{"auth.totp_enable"}
	AuthTOTPDisable      = Action{"auth.totp_disable"}
	AuthTOTPReset        = Action{"auth.totp_reset"}
	AuthTOTPFailed       = Action{"auth.totp_failed"}
	AuthTOTPLockout      = Action{"auth.totp_lockout"}
	AuthTOTPRecoveryUsed = Action{"auth.totp_recovery_used"}
	CLIReset2FA          = Action{"admin.cli_reset_2fa"}
	// Reautenticación antes de lo delicado (§8.2).
	AuthReauth = Action{"auth.reauth"}
	// Cerrar sesiones desde "Mis sesiones" o el panel (§8.2); la consola tiene la suya.
	SessionRevoke = Action{"session.revoke"}
	// Passkeys (§8.1): alta y baja por la persona; el restablecimiento es auth.totp_reset.
	AuthPasskeyAdd    = Action{"auth.passkey_add"}
	AuthPasskeyRemove = Action{"auth.passkey_remove"}
	// Propuestas (§5.2). Aprobar con cambios es proposal.approve con meta.modified.
	ProposalSubmit   = Action{"proposal.submit"}
	ProposalApprove  = Action{"proposal.approve"}
	ProposalReject   = Action{"proposal.reject"}
	ProposalWithdraw = Action{"proposal.withdraw"}
	// Artículos compartidos entre áreas (§4.19): van a la auditoría de las dos áreas. share.end
	// lleva meta.reason: unshared (el origen), removed (el destino), level o deleted (la página).
	ShareRequest = Action{"share.request"}
	ShareApprove = Action{"share.approve"}
	ShareReject  = Action{"share.reject"}
	ShareEnd     = Action{"share.end"}
	// Baja de usuarios (§8.4) y el panel de personas (B15). page.owner_transfer también sale al
	// quitar a alguien de un área; user.pseudonymize es del job (B14, §13.10).
	UserOffboardingStart    = Action{"user.offboarding_start"}
	UserOffboardingComplete = Action{"user.offboarding_complete"}
	UserReactivate          = Action{"user.reactivate"}
	UserSuperadminGrant     = Action{"user.superadmin_grant"}
	UserSuperadminRevoke    = Action{"user.superadmin_revoke"}
	UserPseudonymize        = Action{"user.pseudonymize"}
	PageOwnerTransfer       = Action{"page.owner_transfer"}
	PersonalPurge           = Action{"personal.purge"}
	// Sellados (§6, §11.1): nunca llevan el título, que va cifrado; el objetivo es el id del sellado.
	// El admin de área no ve los que hizo otro admin o el superadmin (§11.2).
	SealedCreate       = Action{"sealed.create"}
	SealedUnlock       = Action{"sealed.unlock"}
	SealedUnlockFailed = Action{"sealed.unlock_failed"}
	SealedAdminOpen    = Action{"sealed.admin_open"}
	SealedPurge        = Action{"sealed.purge"}
	// SealedOwnerTransfer: la baja pasó la autoría de un sellado al admin receptor (§8.4).
	SealedOwnerTransfer = Action{"sealed.owner_transfer"}
	// SealedPasswordSet: quien recibió un sellado en una baja le puso su contraseña (§8.4).
	SealedPasswordSet = Action{"sealed.password_set"}
	// Accesos a un sellado (§6.1, F7.2): el autor o el superadmin los dan y los quitan.
	SealedGrant  = Action{"sealed.grant"}
	SealedRevoke = Action{"sealed.revoke"}
	// VaultSet: la persona creó su clave de bóveda o puso una nueva (§6.3).
	VaultSet = Action{"vault.set"}
	// SealedDeclassify: el superadmin convirtió el sellado en una página del área (§6.1, F7.3).
	SealedDeclassify = Action{"sealed.declassify"}
	// Restablecer la contraseña de un sellado (§6.3, F7.3): el autor pide, el superadmin aprueba
	// y el autor pone la nueva.
	SealedResetRequest  = Action{"sealed.password_reset_request"}
	SealedResetApprove  = Action{"sealed.password_reset_approve"}
	SealedResetComplete = Action{"sealed.password_reset_complete"}
	// MASTER_KEY desde la consola (§6.5, F7.4): rotarla, repartirla en partes y reconstruirla.
	MasterKeyRotate  = Action{"masterkey.rotate"}
	MasterKeySplit   = Action{"masterkey.split"}
	MasterKeyCombine = Action{"masterkey.combine"}
)

// All son todas las acciones; cada una tiene su traducción en la interfaz (lo exige un test).
var All = []Action{
	AuthLogin, AuthLoginFailed, AuthLockout, AuthLogout, AuthPasswordChange, UserProfileUpdate, UserInvite, UserInviteAccept,
	WsCreate, WsUpdate, BackupRun, WsMemberAdd, WsMemberRemove, WsMemberRole,
	PageCreate, PageUpdate, PageRevert, PageMove, PageLevelChange, PageDelete, PageRestore, PagePurge,
	PageRead, PathUnavailable, MediaUpload, MediaDelete, RevisionPurge, AnnouncementCreate, AnnouncementDelete,
	TemplateCreate, TemplateUpdate, TemplateDelete, PageVerify, PageReviewUpdate, PagePublish, PersonalAuditOpen, ReadRequestCreate, ReadRequestClose, ReadAck,
	QuizCreate, QuizUpdate, QuizPublish, QuizArchive, QuizDelete, QuizAttemptStart, QuizAttemptSubmit, QuizAttemptTimeout,
	QuizExtraAttemptsGrant, QuizResultsExport,
	CLIUnlock, CLISetPassword, CLICreateSuperadmin, CLIRevokeSessions,
	SiteBrandingUpdate, SiteAssetUpload, SiteAssetDelete, SiteSMTPUpdate, SMTPTestSend, LDAPConfigUpdate, LDAPTest,
	LDAPMappingCreate, LDAPMappingUpdate, LDAPMappingDelete,
	LDAPAccountLink, LDAPMembershipDetach, LDAPSyncStart, LDAPSyncDone, LDAPSyncAborted, UserDeactivatedByLDAP,
	AuthPasswordResetRequest, AuthPasswordResetComplete,
	AuthTOTPEnable, AuthTOTPDisable, AuthTOTPReset, AuthTOTPFailed, AuthTOTPLockout, AuthTOTPRecoveryUsed, CLIReset2FA, AuthReauth, SessionRevoke, AuthPasskeyAdd, AuthPasskeyRemove,
	ProposalSubmit, ProposalApprove, ProposalReject, ProposalWithdraw,
	ShareRequest, ShareApprove, ShareReject, ShareEnd,
	UserOffboardingStart, UserOffboardingComplete, UserReactivate, UserSuperadminGrant, UserSuperadminRevoke, UserPseudonymize,
	PageOwnerTransfer, PersonalPurge,
	SealedCreate, SealedUnlock, SealedUnlockFailed, SealedAdminOpen, SealedPurge, SealedOwnerTransfer, SealedPasswordSet, SealedGrant, SealedRevoke, VaultSet,
	SealedDeclassify, SealedResetRequest, SealedResetApprove, SealedResetComplete,
	MasterKeyRotate, MasterKeySplit, MasterKeyCombine,
	AuditExport, BackupUpload, BackupVerify, BackupFailed, BackupSettingsUpdate, SiteReset, BackupDelete, BackupDownload,
	AccessReviewConfirm, AccessReviewDue,
	WsArchive, WsUnarchive, WsDeactivate, WsReactivate, WsDeleteRequest, WsDeleteCancel, WsPurge, WsSlugChange,
	PageTransferOut, PageTransferIn, WsExport, WsImport, PrivacyRetention, PrivacyExport,
}

// Actor es quien hace la acción, tal como está en ese momento.
type Actor struct{ ID, Email, Name, IP string }

type actorKey struct{}

// WithActor deja al actor en el contexto del request: los servicios registran sin tener que
// recibir la IP ni el nombre como parámetros.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

// WithActorFrom pasa el actor de from a ctx: una tarea que sigue después de la petición (un
// respaldo pedido desde el panel) vive con ctx pero se registra a nombre de quien la pidió.
func WithActorFrom(ctx, from context.Context) context.Context {
	return WithActor(ctx, actorFrom(from))
}

func actorFrom(ctx context.Context) Actor {
	a, _ := ctx.Value(actorKey{}).(Actor)
	return a
}

// Event es lo que se registra.
type Event struct {
	Action     Action
	WsID       string
	TargetType string // page, user, workspace, media, announcement
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
