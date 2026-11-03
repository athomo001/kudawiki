// Package sealed son los documentos sellados (SPEC §6, ADR-0004, ADR-0011): cifrados con una DEK
// por documento, que se guarda solo envuelta (contraseña, bóveda, MASTER_KEY) y que, ya abierta,
// vive solo en la memoria del servidor, atada a una sesión y a un documento.
package sealed

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/crypto"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/oklog/ulid/v2"
)

var (
	// ErrInvalid: datos que no sirven (ruta, título, tamaño) o una forma de abrir que no le toca.
	ErrInvalid = errors.New("invalid_sealed")
	// ErrPasswordShort: la contraseña del documento tiene menos de 12 caracteres (§6.1).
	ErrPasswordShort = errors.New("sealed_password_short")
	ErrWrongPassword = errors.New("sealed_wrong_password")
	// ErrLocked: el documento no está abierto en esta sesión (o pasaron 15 minutos sin usarlo).
	ErrLocked = errors.New("sealed_locked")
	// ErrReadOnly: quien lo abrió no lo puede cambiar ni borrar.
	ErrReadOnly = errors.New("sealed_read_only")
	// ErrSecondFactor: abrir con la llave maestra exige un segundo factor configurado (§6.2).
	ErrSecondFactor = errors.New("second_factor_required")
	// ErrConfirm: el borrado definitivo pide escribir BORRAR (§6.1).
	ErrConfirm = errors.New("confirm_required")
	// ErrVaultShort: la clave de bóveda tiene menos de 12 caracteres, como la de un documento.
	ErrVaultShort = errors.New("vault_key_short")
	// ErrNoVault: quien abre o recibe el acceso todavía no configuró su clave de bóveda (§6.3).
	ErrNoVault = errors.New("vault_missing")
	// ErrWrongVaultKey: la clave de bóveda no abre su bóveda.
	ErrWrongVaultKey = errors.New("vault_wrong_key")
	// ErrNoUser: el correo no es de una cuenta activa a la que se le pueda dar acceso.
	ErrNoUser = errors.New("sealed_user_not_found")
	// ErrNoReset: no hay una aprobación vigente del superadmin para restablecer la contraseña (§6.3).
	ErrNoReset = errors.New("sealed_reset_not_approved")
)

// RateLimitedError: pasó el tope de contraseñas equivocadas; lleva cuánto esperar.
type RateLimitedError struct{ RetryAfter time.Duration }

func (RateLimitedError) Error() string { return "rate_limited" }

const (
	minPassword = 12
	maxPassword = 1024
	// unlockTTL: el documento abierto se cierra solo tras 15 minutos sin usarlo (§6.2).
	unlockTTL = 15 * time.Minute
	// 5 contraseñas por documento y persona cada 15 minutos (§6.4).
	maxAttempts   = 5
	attemptWindow = 15 * time.Minute
	confirmWord   = "BORRAR"
	// resetTTL: la aprobación del superadmin para restablecer la contraseña vale 24 horas (§6.3).
	resetTTL = 24 * time.Hour
)

type store interface {
	CreateSealed(ctx context.Context, s repo.Scope, d repo.NewSealed) error
	AddSealedKey(ctx context.Context, pageID, grantedBy string, k repo.SealedKey) error
	SealedByID(ctx context.Context, userID string, super bool, id string) (repo.SealedDoc, error)
	SealedInArea(ctx context.Context, s repo.Scope, userID string, super bool) ([]repo.SealedRef, error)
	SealedKeyOf(ctx context.Context, pageID, holderType, userID string) (repo.SealedKey, error)
	SealedRevisionOf(ctx context.Context, pageID string, n int) (repo.SealedRevision, error)
	SealedHistory(ctx context.Context, pageID string) ([]repo.SealedRevision, error)
	SaveSealed(ctx context.Context, pageID, actorID string, base int, payload []byte) error
	DeleteSealed(ctx context.Context, pageID string) error
	AreaAdmins(ctx context.Context, wsID string) ([]string, error)
	UserByEmail(ctx context.Context, email string) (repo.User, error)
	VaultOf(ctx context.Context, userID string) (repo.Vault, error)
	SetVault(ctx context.Context, userID string, v repo.Vault) (int64, error)
	SealedGrants(ctx context.Context, pageID string) ([]repo.SealedGrant, error)
	PutSealedGrant(ctx context.Context, pageID, grantedBy string, k repo.SealedKey) error
	RevokeSealedGrant(ctx context.Context, pageID, userID string) error
	SealedHolders(ctx context.Context, pageID string) ([]string, error)
	MySealedDocs(ctx context.Context, userID string) ([]repo.MySealed, error)
	DeclassifySealed(ctx context.Context, s repo.Scope, pageID, actorID string, level int, w repo.PageWrite) (string, error)
	OwnerRank(ctx context.Context, s repo.Scope, userID string) (int, error)
	RequestSealedReset(ctx context.Context, pageID, userID string, now time.Time) error
	ApproveSealedReset(ctx context.Context, pageID, by string, now, expires time.Time) error
	ResetSealedPassword(ctx context.Context, pageID, actorID string, k repo.SealedKey, now time.Time) error
	AllSealed(ctx context.Context) ([]repo.AdminSealed, error)
	AddNotifications(ctx context.Context, wsID string, ns []repo.NewNotification) error
	AddSealedMedia(ctx context.Context, m repo.SealedMedia, uploadedBy string) error
	SealedMediaOf(ctx context.Context, pageID, id string) (repo.SealedMedia, error)
	SealedMediaList(ctx context.Context, pageID string) ([]repo.SealedMedia, error)
}

// files es el disco de la media (media.Service): los archivos de un sellado van cifrados en
// <id>.enc (F7.6), y al desclasificarlo pasan a ser media del área.
type files interface {
	ReadUpload(filename string, body io.Reader) (name, mime string, data []byte, err error)
	PutEncrypted(wsID, id string, data []byte) error
	ReadEncrypted(wsID, id string) ([]byte, error)
	Remove(wsID, id string) error
	Upload(ctx context.Context, sc repo.Scope, actorID, filename string, body io.Reader) (repo.Media, error)
}

// File es un archivo de un sellado, ya descifrado su nombre y su tipo.
type File struct {
	ID, Name, Mime string
	CreatedAt      time.Time
}

// Content es lo que va cifrado en cada revisión.
type Content struct {
	Title    string `json:"title"`
	Markdown string `json:"markdown"`
}

// View es un sellado abierto: lo que se guarda en claro, una revisión descifrada y si quien lo
// abrió lo puede cambiar.
type View struct {
	Doc      repo.SealedDoc
	Content  Content
	Revision repo.SealedRevision
	CanEdit  bool
}

type Service struct {
	store  store
	files  files
	audit  *audit.Recorder
	master crypto.MasterKey
	log    *slog.Logger
	now    func() time.Time

	mu       sync.Mutex
	open     map[openKey]*unlocked
	attempts map[string]attempt
}

// openKey ata la DEK abierta a una sesión (el hash de su cookie) y a un documento: abrirlo en
// otra pestaña de la misma sesión sirve, en otro navegador no.
type openKey struct{ session, page string }

type unlocked struct {
	dek  []byte
	edit bool
	user string // para cerrarlo a esa persona al quitarle el acceso
	used time.Time
}

type attempt struct {
	count int
	since time.Time
}

func NewService(s store, f files, rec *audit.Recorder, master crypto.MasterKey, log *slog.Logger, now func() time.Time) *Service {
	return &Service{store: s, files: f, audit: rec, master: master, log: log, now: now, open: map[openKey]*unlocked{}, attempts: map[string]attempt{}}
}

// Session es la clave de la sesión en el mapa de documentos abiertos: el hash de su token, para
// que la memoria del servidor no guarde tokens de sesión en claro.
func Session(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Create sella un documento nuevo en el área de sc y lo deja abierto en la sesión de quien lo
// crea. La DEK se envuelve con la contraseña (el autor) y con la MASTER_KEY (admins, §6.2).
func (s *Service) Create(ctx context.Context, sc repo.Scope, actorID, session, slug, password string, c Content) (string, error) {
	c.Title = strings.TrimSpace(c.Title)
	if err := wiki.CheckPage(slug, c.Title, c.Markdown, ""); err != nil {
		return "", ErrInvalid
	}
	if n := utf8.RuneCountInString(password); n < minPassword {
		return "", ErrPasswordShort
	} else if n > maxPassword {
		return "", ErrInvalid
	}
	id := ulid.Make().String()
	dek, err := crypto.NewDEK()
	if err != nil {
		return "", err
	}
	salt, err := crypto.NewSalt()
	if err != nil {
		return "", err
	}
	payload, err := seal(dek, id, 1, c)
	if err != nil {
		return "", err
	}
	byPassword, err := crypto.Seal(crypto.PasswordKEK(password, salt), dek, keyAAD(id, "password"))
	if err != nil {
		return "", err
	}
	byMaster, err := crypto.Seal(s.master.Key, dek, keyAAD(id, "master"))
	if err != nil {
		return "", err
	}
	if err := s.store.CreateSealed(ctx, sc, repo.NewSealed{ID: id, Slug: slug, AuthorID: actorID, Payload: payload, Keys: []repo.SealedKey{
		{Type: "password", Salt: salt, Wrapped: byPassword, Permission: "edit"},
		{Type: "master", Wrapped: byMaster, KeyVersion: s.master.Version},
	}}); errors.Is(err, repo.ErrConflict) {
		// El mismo error que una ruta ocupada por una página que quien escribe no ve (§19.35).
		return "", wiki.ErrPathUnavailable
	} else if err != nil {
		return "", err
	}
	s.remember(session, id, actorID, dek, true)
	s.audit.Record(ctx, audit.Event{Action: audit.SealedCreate, WsID: sc.WsID, TargetType: "sealed", TargetID: id})
	return id, nil
}

// InArea son los sellados del área de sc que u ve, para su árbol.
func (s *Service) InArea(ctx context.Context, sc repo.Scope, u repo.User) ([]repo.SealedRef, error) {
	return s.store.SealedInArea(ctx, sc, u.ID, u.IsGlobalAdmin)
}

// Doc es el sellado si u lo ve, sin abrirlo: para saber cómo pedirle que lo desbloquee.
func (s *Service) Doc(ctx context.Context, u repo.User, session, id string) (repo.SealedDoc, bool, error) {
	d, err := s.store.SealedByID(ctx, u.ID, u.IsGlobalAdmin, id)
	if err != nil {
		return d, false, err
	}
	_, open := s.unlocked(session, id)
	return d, open, nil
}

// UnlockPassword abre el documento con su contraseña: queda abierto para leer y cambiar.
func (s *Service) UnlockPassword(ctx context.Context, u repo.User, session, id, password string) error {
	d, err := s.store.SealedByID(ctx, u.ID, u.IsGlobalAdmin, id)
	if err != nil {
		return err
	}
	key := id + ":" + u.ID
	if err := s.allow(key); err != nil {
		return err
	}
	k, err := s.store.SealedKeyOf(ctx, id, "password", "")
	if errors.Is(err, repo.ErrNotFound) {
		return ErrWrongPassword // sin contraseña (pasó a otro dueño, §8.4): ninguna sirve
	} else if err != nil {
		return err
	}
	dek, err := crypto.Open(crypto.PasswordKEK(password, k.Salt), k.Wrapped, keyAAD(id, "password"))
	if err != nil {
		s.audit.Record(ctx, audit.Event{Action: audit.SealedUnlockFailed, WsID: d.WsID, TargetType: "sealed", TargetID: id})
		return ErrWrongPassword
	}
	s.forget(key)
	s.remember(session, id, u.ID, dek, true)
	s.audit.Record(ctx, audit.Event{Action: audit.SealedUnlock, WsID: d.WsID, TargetType: "sealed", TargetID: id, Meta: map[string]any{"by": "password"}})
	return nil
}

// SetPassword es la primera apertura de un sellado recibido en una baja (§8.4): su contraseña la
// conocía solo quien se fue, así que el nuevo autor lo abre con la MASTER_KEY (con la sesión
// reautenticada, lo exige httpapi) y pone una propia. Queda abierto para leer y cambiar.
func (s *Service) SetPassword(ctx context.Context, u repo.User, session, id, password string) error {
	d, err := s.store.SealedByID(ctx, u.ID, u.IsGlobalAdmin, id)
	if err != nil {
		return err
	}
	if !d.IsAuthor || d.HasPassword {
		return ErrInvalid
	}
	if n := utf8.RuneCountInString(password); n < minPassword {
		return ErrPasswordShort
	} else if n > maxPassword {
		return ErrInvalid
	}
	dek, err := s.masterDEK(ctx, id)
	if err != nil {
		return err
	}
	salt, err := crypto.NewSalt()
	if err != nil {
		return err
	}
	wrapped, err := crypto.Seal(crypto.PasswordKEK(password, salt), dek, keyAAD(id, "password"))
	if err != nil {
		return err
	}
	if err := s.store.AddSealedKey(ctx, id, u.ID, repo.SealedKey{Type: "password", Salt: salt, Wrapped: wrapped, Permission: "edit"}); errors.Is(err, repo.ErrConflict) {
		return ErrInvalid // otra pestaña la puso primero
	} else if err != nil {
		return err
	}
	s.remember(session, id, u.ID, dek, true)
	s.audit.Record(ctx, audit.Event{Action: audit.SealedPasswordSet, WsID: d.WsID, TargetType: "sealed", TargetID: id})
	return nil
}

// UnlockAsAdmin abre el documento con la MASTER_KEY, solo para leer (§6.1: lo cambian el autor y
// quien tiene permiso de edición). Lo puede un admin del área o el superadmin, con la sesión
// reautenticada (lo exige httpapi) y un segundo factor configurado. Queda en la auditoría y el
// autor recibe el aviso (§6.2).
func (s *Service) UnlockAsAdmin(ctx context.Context, u repo.User, session, id string) error {
	d, err := s.store.SealedByID(ctx, u.ID, u.IsGlobalAdmin, id)
	if err != nil {
		return err
	}
	if !d.IsAreaAdmin && !u.IsGlobalAdmin {
		return ErrInvalid
	}
	if !hasSecondFactor(u) {
		return ErrSecondFactor
	}
	dek, err := s.masterDEK(ctx, id)
	if err != nil {
		return err
	}
	s.remember(session, id, u.ID, dek, false)
	s.audit.Record(ctx, audit.Event{Action: audit.SealedAdminOpen, WsID: d.WsID, TargetType: "sealed", TargetID: id})
	if d.AuthorID != u.ID {
		s.notify(ctx, d.WsID, u.ID, []string{d.AuthorID}, "sealed_admin_access", payload{Title: d.Slug, Actor: u.FullName, Sealed: id})
	}
	return nil
}

// Read descifra una revisión (n = 0, la vigente) de un documento abierto en esta sesión.
func (s *Service) Read(ctx context.Context, u repo.User, session, id string, n int) (View, error) {
	d, err := s.store.SealedByID(ctx, u.ID, u.IsGlobalAdmin, id)
	if err != nil {
		return View{}, err
	}
	o, ok := s.unlocked(session, id)
	if !ok {
		return View{}, ErrLocked
	}
	rev, err := s.store.SealedRevisionOf(ctx, id, n)
	if err != nil {
		return View{}, err
	}
	c, err := open(o.dek, id, rev.Number, rev.Payload)
	if err != nil {
		return View{}, err
	}
	return View{Doc: d, Content: c, Revision: rev, CanEdit: o.edit}, nil
}

// History son las revisiones de un documento abierto en esta sesión.
func (s *Service) History(ctx context.Context, u repo.User, session, id string) ([]repo.SealedRevision, error) {
	if _, err := s.store.SealedByID(ctx, u.ID, u.IsGlobalAdmin, id); err != nil {
		return nil, err
	}
	if _, ok := s.unlocked(session, id); !ok {
		return nil, ErrLocked
	}
	return s.store.SealedHistory(ctx, id)
}

// Save guarda una revisión nueva sobre base. No pasa por propuestas (§6.1).
func (s *Service) Save(ctx context.Context, u repo.User, session, id string, base int, c Content) (int, error) {
	d, err := s.store.SealedByID(ctx, u.ID, u.IsGlobalAdmin, id)
	if err != nil {
		return 0, err
	}
	o, ok := s.unlocked(session, id)
	if !ok {
		return 0, ErrLocked
	}
	if !o.edit {
		return 0, ErrReadOnly
	}
	c.Title = strings.TrimSpace(c.Title)
	if err := wiki.CheckPage(d.Slug, c.Title, c.Markdown, ""); err != nil || base < 1 {
		return 0, ErrInvalid
	}
	payload, err := seal(o.dek, id, base+1, c)
	if err != nil {
		return 0, err
	}
	if err := s.store.SaveSealed(ctx, id, u.ID, base, payload); errors.Is(err, repo.ErrConflict) {
		return 0, wiki.ErrConflict // alguien guardó antes: el mismo conflicto que en una página
	} else if err != nil {
		return 0, err
	}
	return base + 1, nil
}

// Delete borra el documento para siempre, sin abrirlo (§6.1): el autor siempre puede, aunque no
// recuerde la contraseña; también un admin del área o el superadmin. Avisa a los admins del área.
func (s *Service) Delete(ctx context.Context, u repo.User, id, confirm string) error {
	d, err := s.store.SealedByID(ctx, u.ID, u.IsGlobalAdmin, id)
	if err != nil {
		return err
	}
	if !d.IsAuthor && !d.IsAreaAdmin && !u.IsGlobalAdmin {
		return ErrReadOnly
	}
	if strings.TrimSpace(confirm) != confirmWord {
		return ErrConfirm
	}
	admins, err := s.store.AreaAdmins(ctx, d.WsID)
	if err != nil {
		return err
	}
	holders, err := s.store.SealedHolders(ctx, id)
	if err != nil {
		return err
	}
	media, err := s.store.SealedMediaList(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteSealed(ctx, id); err != nil {
		return err
	}
	s.close(id, "")
	s.removeFiles(media)
	// Sin título: ya no hay con qué descifrarlo, y no debe quedar en claro en la auditoría (§6.1).
	s.audit.Record(ctx, audit.Event{Action: audit.SealedPurge, WsID: d.WsID, TargetType: "sealed", TargetID: id,
		Meta: map[string]any{"author": d.AuthorID}})
	// También a quienes tenían acceso (§6.1). La bandeja muestra solo los avisos de áreas de las
	// que se es miembro: el de alguien de otra área queda guardado sin verse.
	s.notify(ctx, d.WsID, u.ID, append(admins, holders...), "sealed_purged", payload{Title: d.Slug, Actor: u.FullName})
	return nil
}

// UnlockVault abre un sellado compartido con u con su clave de bóveda (§6.2): queda abierto con
// el permiso que le dieron. Comparte el tope de intentos con la contraseña del documento.
func (s *Service) UnlockVault(ctx context.Context, u repo.User, session, id, vaultKey string) error {
	d, err := s.store.SealedByID(ctx, u.ID, u.IsGlobalAdmin, id)
	if err != nil {
		return err
	}
	if d.Grant == "" {
		return ErrInvalid // ve el documento por otro camino: no tiene una DEK envuelta para su bóveda
	}
	key := id + ":" + u.ID
	if err := s.allow(key); err != nil {
		return err
	}
	v, err := s.store.VaultOf(ctx, u.ID)
	if errors.Is(err, repo.ErrNotFound) {
		return ErrNoVault
	} else if err != nil {
		return err
	}
	k, err := s.store.SealedKeyOf(ctx, id, "user", u.ID)
	if err != nil {
		return err
	}
	dek, err := crypto.OpenWithVault(vaultKey, v.Salt, v.PrivEnc, vaultAAD(u.ID), v.Pub, k.Wrapped)
	if err != nil {
		s.audit.Record(ctx, audit.Event{Action: audit.SealedUnlockFailed, WsID: d.WsID, TargetType: "sealed", TargetID: id, Meta: map[string]any{"by": "vault"}})
		return ErrWrongVaultKey
	}
	s.forget(key)
	s.remember(session, id, u.ID, dek, k.Permission == "edit")
	s.audit.Record(ctx, audit.Event{Action: audit.SealedUnlock, WsID: d.WsID, TargetType: "sealed", TargetID: id, Meta: map[string]any{"by": "vault"}})
	return nil
}

// canGrant: los accesos los dan y los quitan el autor y el superadmin; el admin del área no (§19.1).
func canGrant(d repo.SealedDoc, u repo.User) bool { return d.IsAuthor || u.IsGlobalAdmin }

// Grants son los accesos dados en el sellado, para quien los puede cambiar.
func (s *Service) Grants(ctx context.Context, u repo.User, id string) ([]repo.SealedGrant, error) {
	d, err := s.store.SealedByID(ctx, u.ID, u.IsGlobalAdmin, id)
	if err != nil {
		return nil, err
	}
	if !canGrant(d, u) {
		return nil, ErrReadOnly
	}
	return s.store.SealedGrants(ctx, id)
}

// Grant le da acceso a la persona de ese correo, de cualquier área (§6.1), o le cambia el
// permiso. Hace falta la DEK: el documento tiene que estar abierto en esta sesión. Se envuelve
// para la pública de su bóveda, así que no hace falta que esté conectada (§6.3).
func (s *Service) Grant(ctx context.Context, u repo.User, session, id, email, permission string) error {
	d, err := s.store.SealedByID(ctx, u.ID, u.IsGlobalAdmin, id)
	if err != nil {
		return err
	}
	if !canGrant(d, u) {
		return ErrReadOnly
	}
	if permission != "read" && permission != "edit" {
		return ErrInvalid
	}
	o, ok := s.unlocked(session, id)
	if !ok {
		return ErrLocked
	}
	to, err := s.store.UserByEmail(ctx, strings.TrimSpace(email))
	if errors.Is(err, repo.ErrNotFound) || err == nil && (!to.IsActive || !to.OffboardedAt.IsZero()) {
		return ErrNoUser
	} else if err != nil {
		return err
	}
	if to.ID == d.AuthorID {
		return ErrInvalid // el autor ya lo abre con su contraseña
	}
	v, err := s.store.VaultOf(ctx, to.ID)
	if errors.Is(err, repo.ErrNotFound) {
		return ErrNoVault
	} else if err != nil {
		return err
	}
	wrapped, err := crypto.WrapForVault(v.Pub, o.dek)
	if err != nil {
		return err
	}
	if err := s.store.PutSealedGrant(ctx, id, u.ID, repo.SealedKey{Type: "user", UserID: to.ID, Permission: permission, Wrapped: wrapped}); err != nil {
		return err
	}
	// Un permiso que cambia vale desde la próxima vez que lo abra: se cierra el que tenía abierto.
	s.close(id, to.ID)
	s.audit.Record(ctx, audit.Event{Action: audit.SealedGrant, WsID: d.WsID, TargetType: "sealed", TargetID: id,
		Meta: map[string]any{"user": to.ID, "permission": permission}})
	return nil
}

// Revoke le quita el acceso: se borra su DEK envuelta y se cierra lo que tenía abierto. La DEK
// no se rota (§6.3, decidido 2026-11-02).
func (s *Service) Revoke(ctx context.Context, u repo.User, id, userID string) error {
	d, err := s.store.SealedByID(ctx, u.ID, u.IsGlobalAdmin, id)
	if err != nil {
		return err
	}
	if !canGrant(d, u) {
		return ErrReadOnly
	}
	if err := s.store.RevokeSealedGrant(ctx, id, userID); err != nil {
		return err
	}
	s.close(id, userID)
	s.audit.Record(ctx, audit.Event{Action: audit.SealedRevoke, WsID: d.WsID, TargetType: "sealed", TargetID: id, Meta: map[string]any{"user": userID}})
	return nil
}

// Mine son los sellados que u escribió y los que le compartieron, de todas sus áreas.
func (s *Service) Mine(ctx context.Context, u repo.User) ([]repo.MySealed, error) {
	return s.store.MySealedDocs(ctx, u.ID)
}

// SetVault crea la bóveda de u o la reemplaza si olvidó la clave (httpapi exige la sesión
// reautenticada). Lo compartido con la anterior se pierde y se vuelve a compartir (§6.3).
func (s *Service) SetVault(ctx context.Context, u repo.User, vaultKey string) (int64, error) {
	if n := utf8.RuneCountInString(vaultKey); n < minPassword {
		return 0, ErrVaultShort
	} else if n > maxPassword {
		return 0, ErrInvalid
	}
	pub, priv, salt, err := crypto.NewVault(vaultKey, vaultAAD(u.ID))
	if err != nil {
		return 0, err
	}
	lost, err := s.store.SetVault(ctx, u.ID, repo.Vault{Pub: pub, PrivEnc: priv, Salt: salt})
	if err != nil {
		return 0, err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.VaultSet, TargetType: "user", TargetID: u.ID, Meta: map[string]any{"rotated": u.HasVault, "lost": lost}})
	return lost, nil
}

// ----- La DEK en memoria -----

// ----- Desclasificar y restablecer la contraseña (§6.1, §6.3, F7.3) -----

// Declassify convierte el sellado en una página normal de su área, que se indexa (§6.1). Solo el
// superadmin, con la sesión reautenticada (lo exige httpapi). Descifra la revisión vigente con la
// MASTER_KEY; la página nace con ella como revisión 1 y su autor y responsable es el del sellado.
// El nivel es el más alto que el autor puede leer, su rol en el área: el contenido era secreto,
// bajarlo es una decisión aparte, y el responsable de una página tiene que poder leerla (§4.8).
// Si el autor ya no es del área, N3. Las revisiones anteriores y todas las DEK envueltas se
// borran como en un borrado definitivo. Devuelve el sellado como era, para saber a dónde ir.
func (s *Service) Declassify(ctx context.Context, u repo.User, id string) (repo.SealedDoc, error) {
	d, err := s.store.SealedByID(ctx, u.ID, u.IsGlobalAdmin, id)
	if err != nil {
		return d, err
	}
	if !u.IsGlobalAdmin {
		return d, ErrInvalid
	}
	if !hasSecondFactor(u) {
		return d, ErrSecondFactor
	}
	dek, err := s.masterDEK(ctx, id)
	if err != nil {
		return d, err
	}
	rev, err := s.store.SealedRevisionOf(ctx, id, 0)
	if err != nil {
		return d, err
	}
	c, err := open(dek, id, rev.Number, rev.Payload)
	if err != nil {
		return d, err
	}
	sc := repo.Scope{WsID: d.WsID, MaxRank: 3}
	media, err := s.store.SealedMediaList(ctx, id)
	if err != nil {
		return d, err
	}
	if c.Markdown, err = s.declassifyFiles(ctx, sc, u, d, dek, media, c.Markdown); err != nil {
		return d, err
	}
	w := repo.PageWrite{Slug: d.Slug, Title: c.Title, Markdown: c.Markdown, Kind: "created", AuthorID: d.AuthorID, Lang: "es",
		MediaIDs: wiki.MediaRefs(c.Markdown), Links: wiki.Links(c.Markdown), Index: wiki.IndexDoc(c.Title, "", c.Markdown, "es")}
	level, err := s.store.OwnerRank(ctx, sc, d.AuthorID)
	if err != nil {
		return d, err
	}
	pageID, err := s.store.DeclassifySealed(ctx, sc, id, u.ID, cmp.Or(level, 3), w)
	if err != nil {
		return d, err
	}
	s.close(id, "")
	s.removeFiles(media)
	s.audit.Record(ctx, audit.Event{Action: audit.SealedDeclassify, WsID: d.WsID, TargetType: "sealed", TargetID: id,
		Meta: map[string]any{"page": pageID, "author": d.AuthorID, "level": cmp.Or(level, 3)}})
	return d, nil
}

// RequestReset es el pedido del autor que olvidó la contraseña (§6.3, paso 1). Un sellado
// recibido en una baja no tiene contraseña que restablecer: el autor pone una (SetPassword).
func (s *Service) RequestReset(ctx context.Context, u repo.User, id string) error {
	d, err := s.store.SealedByID(ctx, u.ID, u.IsGlobalAdmin, id)
	if err != nil {
		return err
	}
	if !d.IsAuthor || !d.HasPassword {
		return ErrInvalid
	}
	if err := s.store.RequestSealedReset(ctx, id, u.ID, s.now()); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.SealedResetRequest, WsID: d.WsID, TargetType: "sealed", TargetID: id})
	return nil
}

// ApproveReset es la aprobación del superadmin, con o sin pedido del autor (§6.3, paso 2), con
// la sesión reautenticada (lo exige httpapi). Vale 24 horas y avisa al autor.
func (s *Service) ApproveReset(ctx context.Context, u repo.User, id string) error {
	d, err := s.store.SealedByID(ctx, u.ID, u.IsGlobalAdmin, id)
	if err != nil {
		return err
	}
	if !u.IsGlobalAdmin || !d.HasPassword {
		return ErrInvalid
	}
	if !hasSecondFactor(u) {
		return ErrSecondFactor
	}
	now := s.now()
	if err := s.store.ApproveSealedReset(ctx, id, u.ID, now, now.Add(resetTTL)); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.SealedResetApprove, WsID: d.WsID, TargetType: "sealed", TargetID: id,
		Meta: map[string]any{"requested": d.ResetRequested}})
	s.notify(ctx, d.WsID, u.ID, []string{d.AuthorID}, "sealed_reset_approved", payload{Title: d.Slug, Actor: u.FullName, Sealed: id})
	return nil
}

// CompleteReset es la contraseña nueva del autor, con la aprobación vigente y la sesión
// reautenticada (lo exige httpapi: es la prueba de que es el autor, en lugar de un token por
// correo). La DEK no cambia: se desenvuelve con la MASTER_KEY y se envuelve con la nueva, y el
// superadmin nunca la conoce (§6.3, pasos 3 a 5). Cierra el documento en todas las sesiones y
// lo deja abierto en esta.
func (s *Service) CompleteReset(ctx context.Context, u repo.User, session, id, password string) error {
	d, err := s.store.SealedByID(ctx, u.ID, u.IsGlobalAdmin, id)
	if err != nil {
		return err
	}
	if !d.IsAuthor {
		return ErrInvalid
	}
	if s.ResetState(d.ResetRequested, d.ResetExpires) != "approved" {
		return ErrNoReset
	}
	if n := utf8.RuneCountInString(password); n < minPassword {
		return ErrPasswordShort
	} else if n > maxPassword {
		return ErrInvalid
	}
	dek, err := s.masterDEK(ctx, id)
	if err != nil {
		return err
	}
	salt, err := crypto.NewSalt()
	if err != nil {
		return err
	}
	wrapped, err := crypto.Seal(crypto.PasswordKEK(password, salt), dek, keyAAD(id, "password"))
	if err != nil {
		return err
	}
	err = s.store.ResetSealedPassword(ctx, id, u.ID, repo.SealedKey{Type: "password", Salt: salt, Wrapped: wrapped, Permission: "edit"}, s.now())
	if errors.Is(err, repo.ErrNotFound) {
		return ErrNoReset // venció o se usó entre la lectura y la escritura
	} else if err != nil {
		return err
	}
	s.close(id, "")
	s.forget(id + ":" + u.ID)
	s.remember(session, id, u.ID, dek, true)
	s.audit.Record(ctx, audit.Event{Action: audit.SealedResetComplete, WsID: d.WsID, TargetType: "sealed", TargetID: id})
	return nil
}

// ResetState es en qué va el restablecimiento: approved mientras la aprobación vale, requested
// si el autor lo pidió y falta aprobarlo (o la aprobación venció), o vacío.
func (s *Service) ResetState(requested bool, expires time.Time) string {
	switch {
	case !expires.IsZero() && s.now().Before(expires):
		return "approved"
	case requested:
		return "requested"
	}
	return ""
}

// All son todos los sellados, para la lista del superadmin (httpapi exige el rol).
func (s *Service) All(ctx context.Context) ([]repo.AdminSealed, error) { return s.store.AllSealed(ctx) }

// hasSecondFactor: lo que se hace con la MASTER_KEY pide reautenticarse con contraseña y segundo
// factor, TOTP o passkey, que §8.1 trata igual (§6.2, §8.2).
func hasSecondFactor(u repo.User) bool { return u.TOTPEnabled || u.Passkeys > 0 }

// masterDEK desenvuelve la DEK con la MASTER_KEY.
func (s *Service) masterDEK(ctx context.Context, id string) ([]byte, error) {
	k, err := s.store.SealedKeyOf(ctx, id, "master", "")
	if err != nil {
		return nil, err
	}
	// Si falla es otra llave o una fila alterada: un error del servidor, no de quien pide.
	return crypto.Open(s.master.Key, k.Wrapped, keyAAD(id, "master"))
}

func (s *Service) remember(session, id, userID string, dek []byte, edit bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	s.open[openKey{session, id}] = &unlocked{dek: dek, edit: edit, user: userID, used: s.now()}
}

// unlocked devuelve la DEK abierta y renueva su plazo: cuenta la inactividad, no el tiempo total.
func (s *Service) unlocked(session, id string) (unlocked, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.open[openKey{session, id}]
	if !ok || s.now().Sub(o.used) >= unlockTTL {
		delete(s.open, openKey{session, id})
		return unlocked{}, false
	}
	o.used = s.now()
	return *o, true
}

// close olvida un documento en todas las sesiones; con userID, solo en las de esa persona.
func (s *Service) close(id, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, o := range s.open {
		if k.page == id && (userID == "" || o.user == userID) {
			delete(s.open, k)
		}
	}
}

// sweep saca lo vencido; corre con el candado tomado, al abrir uno nuevo.
func (s *Service) sweep() {
	now := s.now()
	for k, o := range s.open {
		if now.Sub(o.used) >= unlockTTL {
			delete(s.open, k)
		}
	}
}

func (s *Service) allow(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	a := s.attempts[key]
	if now.Sub(a.since) >= attemptWindow {
		a = attempt{since: now}
	}
	if a.count >= maxAttempts {
		return RateLimitedError{RetryAfter: a.since.Add(attemptWindow).Sub(now)}
	}
	a.count++
	s.attempts[key] = a
	return nil
}

func (s *Service) forget(key string) {
	s.mu.Lock()
	delete(s.attempts, key)
	s.mu.Unlock()
}

// ----- Cifrado del contenido -----

// La aad ata cada caja a su lugar: una revisión no se puede presentar como otra ni como la de
// otro documento, y una DEK envuelta no sirve en otra fila (§6.3).
func revisionAAD(id string, n int) []byte { return []byte(id + ":" + strconv.Itoa(n)) }
func keyAAD(id, holder string) []byte     { return []byte(id + ":" + holder) }

// fileAAD y fileMetaAAD atan cada archivo y su nombre a su documento y a su id: uno cifrado no se
// puede hacer pasar por otro, ni de otro sellado.
func fileAAD(id, fileID string) []byte     { return []byte(id + ":file:" + fileID) }
func fileMetaAAD(id, fileID string) []byte { return []byte(id + ":file-meta:" + fileID) }

// MasterAAD es la aad de la DEK envuelta con la MASTER_KEY; la rotación (§6.5) la re-cifra.
func MasterAAD(id string) []byte    { return keyAAD(id, "master") }
func vaultAAD(userID string) []byte { return []byte("vault:" + userID) }

func seal(dek []byte, id string, n int, c Content) ([]byte, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return crypto.Seal(dek, b, revisionAAD(id, n))
}

func open(dek []byte, id string, n int, box []byte) (Content, error) {
	b, err := crypto.Open(dek, box, revisionAAD(id, n))
	if err != nil {
		return Content{}, err
	}
	var c Content
	return c, json.Unmarshal(b, &c)
}

// ----- Avisos -----

// payload de los avisos de sellados. Title lleva la ruta, nunca el título: el aviso se guarda en
// claro y el título del documento va cifrado (§6.3).
type payload struct {
	Title  string `json:"title"`
	Actor  string `json:"actor,omitempty"`
	Sealed string `json:"sealed,omitempty"`
}

// notify avisa a to, menos a quien lo hizo.
func (s *Service) notify(ctx context.Context, wsID, actorID string, to []string, kind string, p payload) {
	b, _ := json.Marshal(p)
	ns := make([]repo.NewNotification, 0, len(to))
	seen := map[string]bool{actorID: true} // un admin que además tenía acceso recibe uno solo
	for _, id := range to {
		if !seen[id] {
			seen[id] = true
			ns = append(ns, repo.NewNotification{UserID: id, Kind: kind, Category: "account", Payload: string(b), MinRank: 1})
		}
	}
	if len(ns) == 0 {
		return
	}
	if err := s.store.AddNotifications(ctx, wsID, ns); err != nil {
		s.log.Error("sealed notice failed", "kind", kind, "err", err)
	}
}

// FileURL es la dirección de un archivo de un sellado en su Markdown. Solo sirve con el
// documento abierto en la sesión, como el resto (§6.2).
func FileURL(id, fileID string) string { return "/api/sealed/" + id + "/files/" + fileID }

var fileRe = regexp.MustCompile(`/api/sealed/([0-9A-HJKMNP-TV-Z]{26})/files/([0-9A-HJKMNP-TV-Z]{26})`)

type fileMeta struct {
	Name string `json:"name"`
	Mime string `json:"mime"`
}

// AddFile cifra un archivo con la DEK del documento y lo guarda (§6.1: su media también se
// cifra). Hace falta tenerlo abierto con permiso de edición, como para guardar. El nombre y el
// tipo van cifrados aparte: el nombre de un archivo ya dice algo del contenido.
func (s *Service) AddFile(ctx context.Context, u repo.User, session, id, filename string, body io.Reader) (File, error) {
	d, err := s.store.SealedByID(ctx, u.ID, u.IsGlobalAdmin, id)
	if err != nil {
		return File{}, err
	}
	o, ok := s.unlocked(session, id)
	if !ok {
		return File{}, ErrLocked
	}
	if !o.edit {
		return File{}, ErrReadOnly
	}
	name, mime, data, err := s.files.ReadUpload(filename, body)
	if err != nil {
		return File{}, err
	}
	f := File{ID: ulid.Make().String(), Name: name, Mime: mime}
	box, err := crypto.Seal(o.dek, data, fileAAD(id, f.ID))
	if err != nil {
		return File{}, err
	}
	meta, _ := json.Marshal(fileMeta{Name: name, Mime: mime})
	metaBox, err := crypto.Seal(o.dek, meta, fileMetaAAD(id, f.ID))
	if err != nil {
		return File{}, err
	}
	if err := s.files.PutEncrypted(d.WsID, f.ID, box); err != nil {
		return File{}, err
	}
	if err := s.store.AddSealedMedia(ctx, repo.SealedMedia{ID: f.ID, PageID: id, Meta: metaBox, Size: int64(len(data))}, u.ID); err != nil {
		_ = s.files.Remove(d.WsID, f.ID)
		return File{}, err
	}
	return f, nil
}

// OpenFile descifra un archivo de un documento abierto en esta sesión; con permiso de lectura
// alcanza. Uno de otro sellado es el 404 de siempre.
func (s *Service) OpenFile(ctx context.Context, u repo.User, session, id, fileID string) (File, []byte, error) {
	if _, err := s.store.SealedByID(ctx, u.ID, u.IsGlobalAdmin, id); err != nil {
		return File{}, nil, err
	}
	o, ok := s.unlocked(session, id)
	if !ok {
		return File{}, nil, ErrLocked
	}
	m, err := s.store.SealedMediaOf(ctx, id, fileID)
	if err != nil {
		return File{}, nil, err
	}
	f, err := openMeta(o.dek, id, m)
	if err != nil {
		return File{}, nil, err
	}
	box, err := s.files.ReadEncrypted(m.WsID, m.ID)
	if err != nil {
		return File{}, nil, err
	}
	data, err := crypto.Open(o.dek, box, fileAAD(id, m.ID))
	return f, data, err
}

func openMeta(dek []byte, id string, m repo.SealedMedia) (File, error) {
	raw, err := crypto.Open(dek, m.Meta, fileMetaAAD(id, m.ID))
	if err != nil {
		return File{}, err
	}
	var meta fileMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return File{}, err
	}
	return File{ID: m.ID, Name: meta.Name, Mime: meta.Mime, CreatedAt: m.CreatedAt}, nil
}

// declassifyFiles pasa a la media del área los archivos que usa la versión vigente y cambia sus
// direcciones en el Markdown. Los que solo usaban versiones anteriores se borran con ellas. Si
// algo falla después, lo copiado queda sin página y la purga diaria lo borra (§4.4).
func (s *Service) declassifyFiles(ctx context.Context, sc repo.Scope, u repo.User, d repo.SealedDoc, dek []byte, list []repo.SealedMedia, md string) (string, error) {
	byID := make(map[string]repo.SealedMedia, len(list))
	for _, m := range list {
		byID[m.ID] = m
	}
	urls := map[string]string{}
	for _, ref := range fileRe.FindAllStringSubmatch(md, -1) {
		m, ok := byID[ref[2]]
		if ref[1] != d.ID || !ok || urls[ref[0]] != "" {
			continue
		}
		f, err := openMeta(dek, d.ID, m)
		if err != nil {
			return "", err
		}
		box, err := s.files.ReadEncrypted(m.WsID, m.ID)
		if err != nil {
			return "", err
		}
		data, err := crypto.Open(dek, box, fileAAD(d.ID, m.ID))
		if err != nil {
			return "", err
		}
		nm, err := s.files.Upload(ctx, sc, u.ID, f.Name, bytes.NewReader(data))
		if err != nil {
			return "", err
		}
		urls[ref[0]] = "/api/w/" + d.WsSlug + "/media/" + nm.ID
	}
	return fileRe.ReplaceAllStringFunc(md, func(u string) string { return cmp.Or(urls[u], u) }), nil
}

// removeFiles borra del disco los .enc de un sellado que ya no está en la base. Uno que no se
// pudo borrar es ilegible sin su DEK, y doctor lo muestra como archivo sin fila (§13.7).
func (s *Service) removeFiles(list []repo.SealedMedia) {
	for _, m := range list {
		if err := s.files.Remove(m.WsID, m.ID); err != nil {
			s.log.Warn("sealed file not removed", "file", m.ID, "err", err)
		}
	}
}
