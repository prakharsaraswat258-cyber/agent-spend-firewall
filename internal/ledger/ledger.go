// Package ledger stores metadata only (tokens, cost, model, time). Never prompts or keys.
package ledger

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"
)

type Ledger struct{ db *sql.DB }

type Call struct {
	RunID, Provider, Model               string
	Input, Output, CacheRead, CacheWrite int64
	CostMicro                            int64
	LatencyMS                            int64
	At                                   time.Time
}

func Open(path string) (*Ledger, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // sqlite: one writer
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS runs(id TEXT PRIMARY KEY, started INTEGER, budget_micro INTEGER);
CREATE TABLE IF NOT EXISTS calls(
 id INTEGER PRIMARY KEY, run_id TEXT, provider TEXT, model TEXT,
 input INTEGER, output INTEGER, cache_read INTEGER, cache_write INTEGER,
 cost_micro INTEGER, latency_ms INTEGER, at INTEGER);
CREATE INDEX IF NOT EXISTS calls_run ON calls(run_id);
CREATE INDEX IF NOT EXISTS calls_at ON calls(at);`)
	if err != nil {
		return nil, err
	}
	return &Ledger{db}, nil
}

func (l *Ledger) Close() error { return l.db.Close() }

func (l *Ledger) Record(c Call) error {
	_, err := l.db.Exec(`INSERT OR IGNORE INTO runs(id,started) VALUES(?,?)`, c.RunID, c.At.Unix())
	if err != nil {
		return err
	}
	_, err = l.db.Exec(`INSERT INTO calls(run_id,provider,model,input,output,cache_read,cache_write,cost_micro,latency_ms,at)
VALUES(?,?,?,?,?,?,?,?,?,?)`, c.RunID, c.Provider, c.Model, c.Input, c.Output, c.CacheRead, c.CacheWrite,
		c.CostMicro, c.LatencyMS, c.At.Unix())
	return err
}

func (l *Ledger) SpentRun(run string) (int64, error) {
	return l.sum(`SELECT COALESCE(SUM(cost_micro),0) FROM calls WHERE run_id=?`, run)
}

func (l *Ledger) SpentSince(t time.Time) (int64, error) {
	return l.sum(`SELECT COALESCE(SUM(cost_micro),0) FROM calls WHERE at>=?`, t.Unix())
}

func (l *Ledger) sum(q string, a any) (int64, error) {
	var n int64
	err := l.db.QueryRow(q, a).Scan(&n)
	return n, err
}
