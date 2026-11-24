package ledger

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func rawDB(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// A ledger from the first release (no schema_migrations, runs without its later
// columns, real data) is adopted: data kept, missing columns added, version 2.
func TestAdoptsLedgerFromFirstRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	rawDB(t, path,
		`CREATE TABLE runs(id TEXT PRIMARY KEY, started INTEGER, budget_micro INTEGER)`,
		`CREATE TABLE calls(id INTEGER PRIMARY KEY, run_id TEXT, provider TEXT, model TEXT, input INTEGER, output INTEGER, cache_read INTEGER, cache_write INTEGER, cost_micro INTEGER, latency_ms INTEGER, at INTEGER)`,
		`INSERT INTO runs(id,started,budget_micro) VALUES('old',1,7000000)`,
		`INSERT INTO calls(run_id,provider,model,input,output,cache_read,cache_write,cost_micro,latency_ms,at) VALUES('old','anthropic','m',1,1,0,0,4242,1,1)`,
	)
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if v := l.Version(); v != 2 {
		t.Fatalf("version %d", v)
	}
	if s, _ := l.SpentRun("old"); s != 4242 {
		t.Fatalf("data lost: %d", s)
	}
	if b, ok := l.RunBudget("old"); !ok || b != 7_000_000 {
		t.Fatal("budget lost")
	}
	// New columns and tables work.
	if err := l.Record(Call{RunID: "old", CostMicro: 1, Step: "s", Estimated: true, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := l.SetStopped("old", true); err != nil {
		t.Fatal(err)
	}
	if err := l.SetPlan("old", []StepRow{{"a", "pending"}}); err != nil {
		t.Fatal(err)
	}
}

// A ledger from this branch before migrations already has some of 0002's columns.
func TestAdoptsHalfUpgradedLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "half.db")
	ms, _ := loadMigrations()
	stmts := statements(ms[0].sql)
	stmts = append(stmts,
		`ALTER TABLE calls ADD COLUMN cache_write_1h INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE runs ADD COLUMN stopped INTEGER NOT NULL DEFAULT 0`,
		`CREATE TABLE settings(key TEXT PRIMARY KEY, value INTEGER)`,
		`INSERT INTO settings VALUES('run_cap', 5000000)`)
	rawDB(t, path, stmts...)
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := l.Setting("run_cap"); v != 5_000_000 || l.Version() != 2 {
		t.Fatal("settings lost or wrong version")
	}
	l.Close()
	// Opening again changes nothing and fails on nothing.
	l, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
}

func TestFreshLedgerRunsEveryMigrationOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.db")
	for i := 0; i < 2; i++ {
		l, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var n int
		l.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n)
		ms, _ := loadMigrations()
		if n != len(ms) || l.Version() != ms[len(ms)-1].version {
			t.Fatalf("applied %d of %d", n, len(ms))
		}
		l.Close()
	}
}
