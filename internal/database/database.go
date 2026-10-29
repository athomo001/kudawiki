// Package database abre SQLite y aplica las migraciones embebidas (SPEC §10, §13.4).
package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // driver sin CGO (SPEC §19.33)
)

//go:embed migrations/*.sql
var migrations embed.FS

// DB separa lectura y escritura: un pool de lectura y una sola conexión de escritura
// serializada, para no chocar con SQLITE_BUSY bajo WAL (SPEC §13.4).
type DB struct {
	Read  *sql.DB
	Write *sql.DB
}

// temp_store(MEMORY): los temporales de SQLite (migraciones, ordenamientos) van a memoria. En el
// contenedor el sistema de archivos es de solo lectura y no siempre hay un /tmp escribible.
const pragmas = "_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)&_pragma=temp_store(MEMORY)"

// Open crea el directorio si falta y abre las dos conexiones.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	// La escritura abre primero: crea el archivo y deja WAL activo antes de que lea nadie.
	// _txlock=immediate toma el lock al empezar la transacción y no a mitad de ella.
	write, err := sql.Open("sqlite", "file:"+path+"?"+pragmas+"&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	write.SetMaxOpenConns(1)
	if err := write.Ping(); err != nil {
		write.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Las lecturas van por mmap (SPEC §13.4): con la caché por omisión de SQLite (~2 MB por
	// conexión), buscar en un área de 50 000 páginas (~800 MB de base) releía el índice en cada
	// consulta, ~110 ms solo para los candidatos; con mmap, ~20 ms (§9.5, medido 2026-10-29). Se
	// prefirió a cache_size porque las páginas mapeadas las guarda el sistema una sola vez para
	// todo el pool, y las libera si le falta memoria; cache_size duplica la caché por conexión.
	read, err := sql.Open("sqlite", "file:"+path+"?mode=ro&"+pragmas+"&_pragma=mmap_size(1073741824)")
	if err != nil {
		write.Close()
		return nil, err
	}
	read.SetMaxOpenConns(max(4, runtime.NumCPU()))
	return &DB{Read: read, Write: write}, nil
}

// Close deja las estadísticas del planificador al día antes de cerrar (SPEC §13.4).
func (db *DB) Close() error {
	_, _ = db.Write.Exec("PRAGMA optimize")
	return errors.Join(db.Read.Close(), db.Write.Close())
}

type migration struct {
	version int
	name    string
}

func embedded() ([]migration, error) {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, e := range entries {
		// Nombre: NNNN_descripcion.sql; el número es la versión.
		n, err := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0])
		if err != nil {
			return nil, fmt.Errorf("migration %s: bad version prefix", e.Name())
		}
		ms = append(ms, migration{n, e.Name()})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	return ms, nil
}

// Migrate aplica en orden las migraciones pendientes, cada una en su transacción:
// si una falla no queda nada a medias (SPEC §13.9).
func (db *DB) Migrate(ctx context.Context) error {
	if _, err := db.Write.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		return err
	}
	current, err := db.version(ctx, db.Write)
	if err != nil {
		return err
	}
	ms, err := embedded()
	if err != nil {
		return err
	}
	// Una sola conexión para todo: el PRAGMA foreign_keys vale por conexión.
	conn, err := db.Write.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	for _, m := range ms {
		if m.version <= current {
			continue
		}
		if err := apply(ctx, conn, m); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
	}
	return nil
}

// noForeignKeys marca las migraciones que reconstruyen una tabla con hijas: con las claves
// foráneas encendidas, su DROP TABLE dispararía los ON DELETE CASCADE de las hijas. SQLite no
// deja cambiarlas dentro de una transacción, así que se apagan antes y la integridad se
// verifica con foreign_key_check antes de confirmar.
const noForeignKeys = "-- foreign_keys: off"

func apply(ctx context.Context, conn *sql.Conn, m migration) (err error) {
	body, err := migrations.ReadFile("migrations/" + m.name)
	if err != nil {
		return err
	}
	noFK := strings.HasPrefix(string(body), noForeignKeys)
	if noFK {
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return err
		}
		defer func() {
			_, onErr := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
			err = errors.Join(err, onErr)
		}()
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	if noFK {
		var table string
		switch err := tx.QueryRowContext(ctx, `PRAGMA foreign_key_check`).Scan(&table, new(any), new(any), new(any)); {
		case err == nil:
			return fmt.Errorf("foreign key violation in %s", table)
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (?)`, m.version); err != nil {
		return err
	}
	return tx.Commit()
}

// Ready confirma que la BD responde y que no quedan migraciones pendientes (/readyz, SPEC §13.5).
func (db *DB) Ready(ctx context.Context) error {
	current, err := db.version(ctx, db.Read)
	if err != nil {
		return err
	}
	ms, err := embedded()
	if err != nil {
		return err
	}
	if latest := ms[len(ms)-1].version; current != latest {
		return fmt.Errorf("schema at version %d, want %d", current, latest)
	}
	return nil
}

func (db *DB) version(ctx context.Context, conn *sql.DB) (int, error) {
	var v sql.NullInt64
	err := conn.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	return int(v.Int64), err
}
