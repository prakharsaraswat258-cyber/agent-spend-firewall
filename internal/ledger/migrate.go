package ledger

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Migrations are numbered SQL files, applied in order, once each, each in its own
// transaction, and recorded in schema_migrations. Never edit a released file: add
// the next number instead.
//
//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	files, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, f := range files {
		base := strings.TrimPrefix(f, "migrations/")
		n, err := strconv.Atoi(strings.SplitN(base, "_", 2)[0])
		if err != nil {
			return nil, fmt.Errorf("migration %s: name must start with a number", base)
		}
		b, err := migrationFS.ReadFile(f)
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{n, base, string(b)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	for i := 1; i < len(ms); i++ {
		if ms[i].version == ms[i-1].version {
			return nil, fmt.Errorf("two migrations numbered %d", ms[i].version)
		}
	}
	return ms, nil
}

func tableExists(db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	return n > 0, err
}

func hasColumn(db *sql.DB, table, col string) (bool, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return false, err
		}
		if strings.EqualFold(n, col) {
			return true, nil
		}
	}
	return false, rows.Err()
}

var addColumn = regexp.MustCompile(`(?is)^\s*ALTER\s+TABLE\s+(\w+)\s+ADD\s+COLUMN\s+(\w+)`)

// statements splits a migration file into statements, dropping comment lines.
// Our SQL has no semicolons inside strings.
func statements(src string) []string {
	var out []string
	for _, s := range strings.Split(src, ";") {
		var keep []string
		for _, line := range strings.Split(s, "\n") {
			if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "--") {
				keep = append(keep, line)
			}
		}
		if len(keep) > 0 {
			out = append(out, strings.Join(keep, "\n"))
		}
	}
	return out
}

// legacyVersion is the last migration that ledgers made before migrations existed
// may already contain, in whole or in part.
const legacyVersion = 2

// legacyRunColumns were added to runs by ALTER in the first releases.
var legacyRunColumns = []string{"repo TEXT", "branch TEXT", "agent TEXT", "dir TEXT", "session_id TEXT",
	"steps_done INTEGER", "steps_total INTEGER", "ended INTEGER", "outcome TEXT"}

func addIfMissing(db *sql.DB, table, colDef string) error {
	ok, err := hasColumn(db, table, strings.Fields(colDef)[0])
	if err != nil || ok {
		return err
	}
	_, err = db.Exec("ALTER TABLE " + table + " ADD COLUMN " + colDef)
	return err
}

// adoptLegacy brings a ledger made before migrations up to legacyVersion without
// failing on what it already has: tables are created if missing, and a column is
// added only when it is missing.
func adoptLegacy(db *sql.DB, ms []migration) error {
	for _, c := range legacyRunColumns {
		if err := addIfMissing(db, "runs", c); err != nil {
			return err
		}
	}
	for _, m := range ms {
		if m.version > legacyVersion {
			break
		}
		for _, st := range statements(m.sql) {
			if mm := addColumn.FindStringSubmatch(st); mm != nil {
				if ok, err := hasColumn(db, mm[1], mm[2]); err != nil {
					return err
				} else if ok {
					continue
				}
			}
			if _, err := db.Exec(st); err != nil {
				return fmt.Errorf("adopting ledger (%s): %w", m.name, err)
			}
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, m.version, time.Now().Unix()); err != nil {
			return err
		}
	}
	return nil
}

// migrate applies every migration the ledger doesn't have yet.
func migrate(db *sql.DB) error {
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	hadMigrations, err := tableExists(db, "schema_migrations")
	if err != nil {
		return err
	}
	hadRuns, err := tableExists(db, "runs")
	if err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER)`); err != nil {
		return err
	}
	if !hadMigrations && hadRuns {
		if err := adoptLegacy(db, ms); err != nil {
			return err
		}
	}
	var current int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}
	for _, m := range ms {
		if m.version <= current {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		for _, st := range statements(m.sql) {
			if _, err := tx.Exec(st); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %s: %w", m.name, err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, m.version, time.Now().Unix()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Version is the newest migration applied (shown by doctor).
func (l *Ledger) Version() int {
	var v int
	l.db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&v)
	return v
}
