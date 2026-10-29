// Package repo es la única capa con SQL (SPEC §3.3). Usuarios y sesiones son globales, no
// contenido de un área, por eso estas funciones no llevan Scope; todo lo de contenido sí lo llevará (F2).
package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/athomo001/kudawiki/internal/database"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var (
	// ErrNotFound es el único error de "no existe"; quien llama decide cómo responder.
	ErrNotFound = errors.New("not found")
	// ErrConflict: violación de unicidad (email o slug ya usados).
	ErrConflict = errors.New("conflict")
)

// conflict traduce la violación de UNIQUE del driver a ErrConflict.
func conflict(err error) error {
	var se *sqlite.Error
	if errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return ErrConflict
	}
	return err
}

type Repo struct{ db *database.DB }

func New(db *database.DB) *Repo { return &Repo{db: db} }

type User struct {
	ID            string
	Email         string
	FullName      string
	PasswordHash  string // vacío si no tiene contraseña local
	IsGlobalAdmin bool
	IsActive      bool
	Locale        string
	ReadingPrefs  ReadingPrefs
	Invited       bool // creado por invitación y todavía sin contraseña (SPEC §8.5.3)
}

// ReadingPrefs son las preferencias de lectura de SPEC §15. Los valores los valida auth.
type ReadingPrefs struct {
	Font string `json:"font"` // default | dyslexic | legible
	Wide bool   `json:"wide"`
}

type Session struct {
	TokenHash  string
	UserID     string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

func (r *Repo) CreateUser(ctx context.Context, u User) error {
	_, err := r.db.Write.ExecContext(ctx,
		`INSERT INTO users (id, email, full_name, password_hash, is_global_admin, is_active, locale, invited_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, CASE WHEN ? THEN CURRENT_TIMESTAMP END)`,
		u.ID, u.Email, u.FullName, sql.NullString{String: u.PasswordHash, Valid: u.PasswordHash != ""},
		u.IsGlobalAdmin, u.IsActive, u.Locale, u.Invited)
	return conflict(err)
}

const userColumns = `id, email, full_name, COALESCE(password_hash, ''), is_global_admin, is_active, locale, reading_prefs_json, invited_at IS NOT NULL`

func (r *Repo) UserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(r.db.Read.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE email = ?`, email))
}

func (r *Repo) UserByID(ctx context.Context, id string) (User, error) {
	return scanUser(r.db.Read.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

// ListUsers es para el panel del superadmin: los usuarios no son contenido de un área.
func (r *Repo) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY full_name`)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanUser)
}

type scanner interface{ Scan(dest ...any) error }

func scanUser(row scanner) (User, error) {
	var u User
	var prefs string
	err := row.Scan(&u.ID, &u.Email, &u.FullName, &u.PasswordHash, &u.IsGlobalAdmin, &u.IsActive, &u.Locale, &prefs, &u.Invited)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	// '{}' (la fila nunca tocó sus preferencias) deja Font vacío: es la predeterminada.
	if err := json.Unmarshal([]byte(prefs), &u.ReadingPrefs); err != nil {
		return User{}, err
	}
	if u.ReadingPrefs.Font == "" {
		u.ReadingPrefs.Font = "default"
	}
	return u, nil
}

// SetPreferences cambia el idioma y las preferencias de lectura de una persona (SPEC §15).
func (r *Repo) SetPreferences(ctx context.Context, userID, locale string, prefs ReadingPrefs) error {
	b, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	res, err := r.db.Write.ExecContext(ctx,
		`UPDATE users SET locale = ?, reading_prefs_json = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, locale, string(b), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// collect recorre las filas con la misma función de lectura que usan las consultas de una fila.
func collect[T any](rows *sql.Rows, scan func(scanner) (T, error)) ([]T, error) {
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Repo) CreateSession(ctx context.Context, s Session) error {
	_, err := r.db.Write.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, last_seen_at) VALUES (?, ?, ?, ?)`,
		s.TokenHash, s.UserID, s.CreatedAt.UTC(), s.LastSeenAt.UTC())
	return err
}

func (r *Repo) SessionByHash(ctx context.Context, tokenHash string) (Session, error) {
	s := Session{TokenHash: tokenHash}
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT user_id, created_at, last_seen_at FROM sessions WHERE token_hash = ?`, tokenHash).
		Scan(&s.UserID, &s.CreatedAt, &s.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return s, err
}

func (r *Repo) TouchSession(ctx context.Context, tokenHash string, at time.Time) error {
	_, err := r.db.Write.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?`, at.UTC(), tokenHash)
	return err
}

func (r *Repo) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := r.db.Write.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteSessions cierra las sesiones de una persona, o las de todos si userID está vacío
// (SPEC §13.6). Devuelve cuántas cerró.
func (r *Repo) DeleteSessions(ctx context.Context, userID string) (int64, error) {
	res, err := r.db.Write.ExecContext(ctx, `DELETE FROM sessions WHERE ? = '' OR user_id = ?`, userID, userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetPassword fija la contraseña de una persona desde la consola (SPEC §13.6). Como al
// restablecerla (§8.5.1), en la misma transacción se cierran sus sesiones y se anulan sus
// enlaces sin usar: una invitación pendiente ya no sirve para elegir otra.
func (r *Repo) SetPassword(ctx context.Context, userID, passwordHash string) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, invited_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, passwordHash, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	for _, q := range []string{`DELETE FROM auth_tokens WHERE user_id = ? AND used_at IS NULL`, `DELETE FROM sessions WHERE user_id = ?`} {
		if _, err := tx.ExecContext(ctx, q, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UnlockLogin deja la hora del desbloqueo (SPEC §13.6); el servidor la lee al ver el bloqueo.
func (r *Repo) UnlockLogin(ctx context.Context, userID string, at time.Time) error {
	_, err := r.db.Write.ExecContext(ctx, `UPDATE users SET login_unlocked_at = ? WHERE id = ?`, at.UTC(), userID)
	return err
}

// LoginUnlockedAt es la hora del último desbloqueo por consola; cero si nunca hubo uno o si el
// email no existe.
func (r *Repo) LoginUnlockedAt(ctx context.Context, email string) (time.Time, error) {
	var at sql.NullTime
	err := r.db.Read.QueryRowContext(ctx, `SELECT login_unlocked_at FROM users WHERE email = ?`, email).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	return at.Time, err
}
