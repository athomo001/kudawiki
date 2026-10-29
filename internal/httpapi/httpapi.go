// Package httpapi traduce HTTP ↔ dominio: sin reglas de negocio ni SQL (SPEC §16.2).
// Las lecturas sin reglas van directo a repo; lo que tiene reglas pasa por su servicio.
package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/auth"
	"github.com/athomo001/kudawiki/internal/media"
	"github.com/athomo001/kudawiki/internal/middleware"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/athomo001/kudawiki/internal/workspace"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

type authService interface {
	Login(ctx context.Context, ip, email, password string) (string, error)
	Authenticate(ctx context.Context, token string) (repo.User, error)
	Logout(ctx context.Context, token string) error
	Invite(ctx context.Context, actorID, email, fullName string) (string, error)
	ResendInvite(ctx context.Context, actorID, userID string) (string, error)
	AcceptInvite(ctx context.Context, token, password string) error
	SetPreferences(ctx context.Context, userID string, p auth.Preferences) error
}

type workspaceService interface {
	Create(ctx context.Context, slug, name string) (repo.Workspace, error)
	Update(ctx context.Context, sc repo.Scope, name, description *string) error
	SetMember(ctx context.Context, a workspace.Actor, sc repo.Scope, email, role string) error
	RemoveMember(ctx context.Context, a workspace.Actor, sc repo.Scope, userID string) error
}

type wikiService interface {
	Get(ctx context.Context, sc repo.Scope, slug string) (repo.Page, error)
	Save(ctx context.Context, sc repo.Scope, actorID string, base int, w repo.PageWrite) error
	Revert(ctx context.Context, sc repo.Scope, actorID, slug string, n, base int) error
	SetTags(ctx context.Context, sc repo.Scope, slug string, tags []string) error
	SetInfo(ctx context.Context, sc repo.Scope, slug string, description, icon *string) error
	Move(ctx context.Context, sc repo.Scope, actorID, from, to string) error
	SetLevel(ctx context.Context, sc repo.Scope, actorID, slug string, level int) error
	Delete(ctx context.Context, sc repo.Scope, actorID, slug string) error
	Restore(ctx context.Context, sc repo.Scope, actorID, id string) error
}

type mediaService interface {
	Upload(ctx context.Context, sc repo.Scope, actorID, filename string, body io.Reader) (repo.Media, error)
	Open(ctx context.Context, sc repo.Scope, id string, thumb bool) (repo.Media, *os.File, error)
}

// reader son las consultas sin reglas de negocio que usan los handlers.
type reader interface {
	ListUsers(ctx context.Context) ([]repo.User, error)
	ListWorkspaces(ctx context.Context) ([]repo.Workspace, error)
	WorkspaceByID(ctx context.Context, id string) (repo.Workspace, error)
	Memberships(ctx context.Context, userID string) ([]repo.Membership, error)
	MembershipBySlug(ctx context.Context, slug, userID string) (repo.Membership, error)
	Members(ctx context.Context, s repo.Scope) ([]repo.Member, error)
	Tree(ctx context.Context, s repo.Scope) ([]repo.PageRef, error)
	PageBySlug(ctx context.Context, s repo.Scope, slug string) (repo.Page, error)
	Revisions(ctx context.Context, s repo.Scope, pageID string) ([]repo.Revision, error)
	RevisionByNumber(ctx context.Context, s repo.Scope, pageID string, n int) (repo.Revision, error)
	PageTags(ctx context.Context, s repo.Scope, pageID string) ([]string, error)
	Tags(ctx context.Context, s repo.Scope) ([]repo.TagCount, error)
	PagesByTag(ctx context.Context, s repo.Scope, tag string) ([]repo.PageRef, error)
	Backlinks(ctx context.Context, s repo.Scope, pageID string) ([]repo.PageRef, error)
	PageByAlias(ctx context.Context, s repo.Scope, oldSlug string) (string, error)
	Search(ctx context.Context, s repo.Scope, q repo.SearchQuery, limit int) ([]repo.SearchHit, error)
	Snippets(ctx context.Context, s repo.Scope, q repo.SearchQuery, hits []repo.SearchHit) error
	SearchMeta(ctx context.Context, s repo.Scope, pageIDs []string) (tags, authors map[string][]string, err error)
	SuggestTitle(ctx context.Context, s repo.Scope, words []string) (string, error)
	RecordMiss(ctx context.Context, s repo.Scope, query string) error
	Misses(ctx context.Context, s repo.Scope) ([]repo.Miss, error)
	Trash(ctx context.Context, s repo.Scope, actorID string) ([]repo.TrashItem, error)
	AuditEvents(ctx context.Context, f repo.AuditFilter) ([]repo.AuditEntry, error)
	AreaAudit(ctx context.Context, s repo.Scope, f repo.AuditFilter) ([]repo.AuditEntry, error)
	// Salud del contenido (§13.7): lo que pide health.Check.
	BrokenLinks(ctx context.Context, s repo.Scope) ([]repo.BrokenLink, error)
	ShadowedAliases(ctx context.Context, s repo.Scope) ([]repo.ShadowedAlias, error)
	OrphanPages(ctx context.Context, s repo.Scope) ([]string, error)
	PagesListingChildren(ctx context.Context, s repo.Scope) ([]string, error)
	MediaRows(ctx context.Context, s repo.Scope, unattachedBefore time.Time) ([]repo.MediaRow, error)
	PagesWithoutIndex(ctx context.Context, s repo.Scope) ([]string, error)
	OrphanIndexRows(ctx context.Context) (int, error)
}

type readiness interface {
	Ready(ctx context.Context) error
}

type Deps struct {
	Auth       authService
	Workspaces workspaceService
	Wiki       wikiService
	Media      mediaService
	Read       reader
	DB         readiness
	UI         fs.FS // contenido de ui/dist
	BaseURL    *url.URL
	// TrashRetention dice cuándo se purga lo que está en la papelera (SPEC §4.5).
	TrashRetention time.Duration
	// StorageDir es la carpeta de los archivos subidos: la salud la compara con la BD (§13.7).
	StorageDir string
	// RateLimit es el límite general de la API (§14.5); sin indicarlo, rige el de omisión.
	RateLimit middleware.Limit
	Log       *slog.Logger
}

type api struct{ Deps }

// New arma el router con el pipeline de SPEC §14.5.
func New(d Deps) http.Handler {
	a := api{d}
	r := chi.NewRouter()
	r.Use(middleware.Log(d.Log), middleware.SecurityHeaders(d.BaseURL.Scheme == "https"),
		middleware.CSRF(d.BaseURL.Scheme+"://"+d.BaseURL.Host),
		chimw.GetHead) // HEAD responde como GET, sin cuerpo (HTTP)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/readyz", a.readyz)

	r.Route("/api", func(r chi.Router) {
		// La IP de quien pide va al contexto para la auditoría (§11.2); la sesión agrega quién es.
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(audit.WithActor(r.Context(), audit.Actor{IP: clientIP(r)})))
			})
		})
		limit := d.RateLimit
		if limit == (middleware.Limit{}) {
			limit = middleware.DefaultLimit
		}
		r.Use(middleware.RateLimit(limit, time.Now))
		r.Post("/auth/login", a.login)
		r.Post("/auth/invite/accept", a.acceptInvite)
		r.Group(func(r chi.Router) {
			r.Use(a.requireSession)
			r.Post("/auth/logout", a.logout)
			r.Get("/auth/me", a.me)
			r.Patch("/me", a.setPreferences)
			r.Route("/admin", a.adminRoutes)
			r.Route("/w/{ws}", a.areaRoutes)
		})
		r.NotFound(a.notFound)
	})

	r.Get("/*", a.spa)
	return r
}

func (a api) readyz(w http.ResponseWriter, r *http.Request) {
	if err := a.DB.Ready(r.Context()); err != nil {
		a.error(w, r, http.StatusServiceUnavailable, "not_ready", err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// spa sirve el frontend embebido; cualquier ruta que no sea un archivo devuelve index.html
// para que el router del cliente la resuelva.
func (a api) spa(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if f, err := fs.Stat(a.UI, name); err != nil || f.IsDir() || name == "" || name == "index.html" {
		a.index(w, r)
		return
	}
	h := w.Header()
	switch {
	case strings.HasPrefix(name, "assets/"):
		h.Set("Cache-Control", "public, max-age=31536000, immutable") // nombres con hash de Vite
		// El iframe de Mermaid tiene origen opaco y pide sus módulos en modo CORS. Es el bundle
		// público de la interfaz: no hay nada que proteger aquí.
		h.Set("Access-Control-Allow-Origin", "*")
	case strings.HasPrefix(name, "sandbox/"):
		h.Set("Cache-Control", "no-cache")
		// Mermaid necesita estilos en línea: solo esta página los tiene, aislada en un iframe
		// sin acceso a la sesión ni a la API; la CSP de la wiki sigue estricta (SPEC §4.10).
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
			"img-src 'self' data:; font-src 'self'; frame-ancestors 'self'; sandbox allow-scripts")
	default:
		h.Set("Cache-Control", "no-cache")
	}
	http.ServeFileFS(w, r, a.UI, name)
}

// index entrega index.html con un nonce nuevo en cada respuesta: la CSP solo deja pasar los
// <style> que lo llevan, y la interfaz lo lee del <meta> para dárselo al editor.
func (a api) index(w http.ResponseWriter, r *http.Request) {
	page, err := fs.ReadFile(a.UI, "index.html")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		a.fail(w, r, err)
		return
	}
	nonce := base64.RawStdEncoding.EncodeToString(raw)
	h := w.Header()
	h.Set("Content-Security-Policy", middleware.CSP(nonce))
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store") // el nonce no se puede reusar desde una cache
	w.Write(bytes.Replace(page, []byte("<head>"), []byte(`<head><meta name="csp-nonce" content="`+nonce+`">`), 1))
}

// decode limita el cuerpo y rechaza campos desconocidos.
func (a api) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		a.error(w, r, http.StatusBadRequest, "invalid_request", nil)
		return false
	}
	return true
}

func (a api) json(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// notFound es la única respuesta para "no existe" y "no te corresponde" (SPEC §3.3).
func (a api) notFound(w http.ResponseWriter, r *http.Request) {
	a.error(w, r, http.StatusNotFound, "not_found", nil)
}

// error responde con el formato de SPEC §14; el código es estable y la UI lo traduce.
// El detalle interno solo va al log.
func (a api) error(w http.ResponseWriter, r *http.Request, status int, code string, err error) {
	if err != nil {
		a.Log.Error("request failed", "request_id", middleware.RequestID(r.Context()), "code", code, "err", err)
	}
	a.json(w, status, map[string]any{"error": map[string]string{"code": code, "message": http.StatusText(status)}})
}

// domainErrors: el texto de cada error de dominio es su código estable.
var domainErrors = map[error]int{
	auth.ErrInvalidCredentials: http.StatusUnauthorized,
	auth.ErrPasswordTooShort:   http.StatusUnprocessableEntity,
	auth.ErrPasswordCommon:     http.StatusUnprocessableEntity,
	auth.ErrInvalidUser:        http.StatusUnprocessableEntity,
	auth.ErrEmailTaken:         http.StatusConflict,
	auth.ErrInvalidInvite:      http.StatusGone,
	auth.ErrNotInvited:         http.StatusConflict,
	auth.ErrInvalidPreferences: http.StatusUnprocessableEntity,
	workspace.ErrInvalidArea:   http.StatusUnprocessableEntity,
	workspace.ErrSlugTaken:     http.StatusConflict,
	workspace.ErrInvalidRole:   http.StatusUnprocessableEntity,
	workspace.ErrUserNotFound:  http.StatusUnprocessableEntity,
	workspace.ErrForbidden:     http.StatusForbidden,
	wiki.ErrInvalidPage:        http.StatusUnprocessableEntity,
	wiki.ErrInvalidInfo:        http.StatusUnprocessableEntity,
	wiki.ErrConflict:           http.StatusConflict,
	wiki.ErrPathUnavailable:    http.StatusConflict,
	wiki.ErrInvalidTags:        http.StatusUnprocessableEntity,
	wiki.ErrInvalidMove:        http.StatusUnprocessableEntity,
	wiki.ErrInvalidLevel:       http.StatusUnprocessableEntity,
	wiki.ErrLevelBelowParent:   http.StatusUnprocessableEntity,
	media.ErrTooLarge:          http.StatusRequestEntityTooLarge,
	media.ErrUnsupportedType:   http.StatusUnsupportedMediaType,
}

// fail traduce un error de dominio a su respuesta; cualquier otro es un 500.
// repo.ErrNotFound es siempre el 404 uniforme (SPEC §3.3).
func (a api) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, repo.ErrNotFound) {
		a.notFound(w, r)
		return
	}
	for e, status := range domainErrors {
		if errors.Is(err, e) {
			a.error(w, r, status, e.Error(), nil)
			return
		}
	}
	a.error(w, r, http.StatusInternalServerError, "internal", err)
}

// clientIP usa la dirección de la conexión; confiar en X-Forwarded-For llega con trust_proxy (§21.2).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
