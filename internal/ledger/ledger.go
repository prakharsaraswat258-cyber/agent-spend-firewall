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
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // sqlite: one writer
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS runs(id TEXT PRIMARY KEY, started INTEGER, budget_micro INTEGER, repo TEXT, branch TEXT, agent TEXT, dir TEXT, session_id TEXT, steps_done INTEGER, steps_total INTEGER, ended INTEGER, outcome TEXT);
CREATE TABLE IF NOT EXISTS forecasts(id INTEGER PRIMARY KEY, run_id TEXT, at INTEGER, spent_micro INTEGER, p50_micro INTEGER, p90_micro INTEGER, confidence TEXT, steps_done INTEGER, steps_total INTEGER);
CREATE TABLE IF NOT EXISTS checkpoints(id INTEGER PRIMARY KEY, run_id TEXT, at INTEGER, branch TEXT, commit_sha TEXT, reason TEXT);
CREATE TABLE IF NOT EXISTS calls(
 id INTEGER PRIMARY KEY, run_id TEXT, provider TEXT, model TEXT,
 input INTEGER, output INTEGER, cache_read INTEGER, cache_write INTEGER,
 cost_micro INTEGER, latency_ms INTEGER, at INTEGER);
CREATE TABLE IF NOT EXISTS loop_alerts(id INTEGER PRIMARY KEY, run_id TEXT, at INTEGER, kind TEXT, tool TEXT, repeats INTEGER, action TEXT);
CREATE TABLE IF NOT EXISTS decisions(id INTEGER PRIMARY KEY, run_id TEXT, asked_at INTEGER, reason TEXT, spent_micro INTEGER, budget_micro INTEGER, p50_micro INTEGER, p90_micro INTEGER, offered_micro INTEGER, answer TEXT, added_micro INTEGER, channel TEXT, response_ms INTEGER);
CREATE INDEX IF NOT EXISTS calls_run ON calls(run_id);
CREATE INDEX IF NOT EXISTS calls_at ON calls(at);`)
	if err != nil {
		return nil, err
	}
	// Older ledgers lack newer run columns; duplicate-column errors are expected and ignored.
	for _, c := range []string{"repo TEXT", "branch TEXT", "agent TEXT", "dir TEXT", "session_id TEXT", "steps_done INTEGER", "steps_total INTEGER", "ended INTEGER", "outcome TEXT"} {
		db.Exec("ALTER TABLE runs ADD COLUMN " + c)
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

type Run struct {
	ID, Repo, Branch, Agent, Dir, SessionID string
	StepsDone, StepsTotal                   int
	LastCall                                time.Time
	Started                                 time.Time
	CostMicro, Calls, Loops                 int64
}

// StartRun registers a run with auto-detected context. Safe to call twice.
func (l *Ledger) StartRun(r Run) error {
	_, err := l.db.Exec(`INSERT OR IGNORE INTO runs(id,started,repo,branch,agent,dir) VALUES(?,?,?,?,?,?)`,
		r.ID, r.Started.Unix(), r.Repo, r.Branch, r.Agent, r.Dir)
	return err
}

// Runs lists newest first with total cost and call count.
func (l *Ledger) Runs(limit int) ([]Run, error) {
	rows, err := l.db.Query(`SELECT r.id, COALESCE(r.repo,''), COALESCE(r.branch,''), COALESCE(r.agent,''), r.started,
 COALESCE(SUM(c.cost_micro),0), COUNT(c.id),
 (SELECT COUNT(*) FROM loop_alerts a WHERE a.run_id=r.id),
 COALESCE(r.steps_done,0), COALESCE(r.steps_total,0), COALESCE(MAX(c.at),0)
FROM runs r LEFT JOIN calls c ON c.run_id=r.id GROUP BY r.id ORDER BY r.started DESC, r.rowid DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		var t, lc int64
		if err := rows.Scan(&r.ID, &r.Repo, &r.Branch, &r.Agent, &t, &r.CostMicro, &r.Calls, &r.Loops, &r.StepsDone, &r.StepsTotal, &lc); err != nil {
			return nil, err
		}
		r.Started, r.LastCall = time.Unix(t, 0), time.Unix(lc, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecordLoop stores a loop alert. Metadata only: tool name, never inputs.
func (l *Ledger) RecordLoop(run, kind, tool string, repeats int, action string) error {
	_, err := l.db.Exec(`INSERT OR IGNORE INTO runs(id,started) VALUES(?,?)`, run, time.Now().Unix())
	if err != nil {
		return err
	}
	_, err = l.db.Exec(`INSERT INTO loop_alerts(run_id,at,kind,tool,repeats,action) VALUES(?,?,?,?,?,?)`,
		run, time.Now().Unix(), kind, tool, repeats, action)
	return err
}

// GetRun returns one run's context, or sql.ErrNoRows.
func (l *Ledger) GetRun(id string) (Run, error) {
	r := Run{ID: id}
	var t int64
	err := l.db.QueryRow(`SELECT COALESCE(repo,''),COALESCE(branch,''),COALESCE(agent,''),COALESCE(dir,''),COALESCE(session_id,''),started,COALESCE(steps_done,0),COALESCE(steps_total,0) FROM runs WHERE id=?`, id).
		Scan(&r.Repo, &r.Branch, &r.Agent, &r.Dir, &r.SessionID, &t, &r.StepsDone, &r.StepsTotal)
	r.Started = time.Unix(t, 0)
	return r, err
}

func (l *Ledger) SetSession(run, sid string) error {
	_, err := l.db.Exec(`UPDATE runs SET session_id=? WHERE id=? AND COALESCE(session_id,'')<>?`, sid, run, sid)
	return err
}

func (l *Ledger) SaveCheckpoint(run, branch, commit, reason string) error {
	_, err := l.db.Exec(`INSERT INTO checkpoints(run_id,at,branch,commit_sha,reason) VALUES(?,?,?,?,?)`,
		run, time.Now().Unix(), branch, commit, reason)
	return err
}

// LastCheckpoint returns branch and commit, or empty strings.
func (l *Ledger) LastCheckpoint(run string) (branch, commit string) {
	l.db.QueryRow(`SELECT branch,commit_sha FROM checkpoints WHERE run_id=? ORDER BY id DESC LIMIT 1`, run).Scan(&branch, &commit)
	return
}

// SetProgress stores the agent's plan state; changed is true when it differs from before.
func (l *Ledger) SetProgress(run string, done, total int) (changed bool, err error) {
	if _, err = l.db.Exec(`INSERT OR IGNORE INTO runs(id,started) VALUES(?,?)`, run, time.Now().Unix()); err != nil {
		return false, err
	}
	res, err := l.db.Exec(`UPDATE runs SET steps_done=?, steps_total=? WHERE id=? AND (COALESCE(steps_done,-1)<>? OR COALESCE(steps_total,-1)<>?)`,
		done, total, run, done, total)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// CallCosts returns per-call costs in call order.
func (l *Ledger) CallCosts(run string) ([]int64, error) {
	rows, err := l.db.Query(`SELECT cost_micro FROM calls WHERE run_id=? ORDER BY id`, run)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var c int64
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveForecast keeps every forecast shown, so accuracy can be scored when the run ends.
func (l *Ledger) SaveForecast(run string, spent, p50, p90 int64, conf string, done, total int) error {
	_, err := l.db.Exec(`INSERT INTO forecasts(run_id,at,spent_micro,p50_micro,p90_micro,confidence,steps_done,steps_total) VALUES(?,?,?,?,?,?,?,?)`,
		run, time.Now().Unix(), spent, p50, p90, conf, done, total)
	return err
}

type Decision struct {
	Run, Reason, Answer, Channel                        string
	Asked                                               time.Time
	Spent, Budget, P50, P90, Offered, Added, ResponseMS int64
}

func (l *Ledger) SaveDecision(d Decision) error {
	_, err := l.db.Exec(`INSERT INTO decisions(run_id,asked_at,reason,spent_micro,budget_micro,p50_micro,p90_micro,offered_micro,answer,added_micro,channel,response_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.Run, d.Asked.Unix(), d.Reason, d.Spent, d.Budget, d.P50, d.P90, d.Offered, d.Answer, d.Added, d.Channel, d.ResponseMS)
	return err
}

type CallRow struct {
	At                                   int64
	Model                                string
	Input, Output, CacheRead, CacheWrite int64
	CostMicro                            int64
}

// Calls returns the newest calls of a run (metadata only).
func (l *Ledger) Calls(run string, limit int) ([]CallRow, error) {
	rows, err := l.db.Query(`SELECT at,model,input,output,cache_read,cache_write,cost_micro FROM calls WHERE run_id=? ORDER BY id DESC LIMIT ?`, run, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CallRow
	for rows.Next() {
		var c CallRow
		if err := rows.Scan(&c.At, &c.Model, &c.Input, &c.Output, &c.CacheRead, &c.CacheWrite, &c.CostMicro); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// EndRun marks a run over. Outcome: "finished" (plan complete), "stopped" (plan incomplete), "ended" (no plan seen).
func (l *Ledger) EndRun(id string) (outcome string, err error) {
	r, err := l.GetRun(id)
	if err != nil {
		return "", err
	}
	switch {
	case r.StepsTotal == 0:
		outcome = "ended"
	case r.StepsDone >= r.StepsTotal:
		outcome = "finished"
	default:
		outcome = "stopped"
	}
	_, err = l.db.Exec(`UPDATE runs SET ended=?, outcome=? WHERE id=?`, time.Now().Unix(), outcome, id)
	return outcome, err
}

type AccRun struct {
	ID                string
	AtSpent, P90, Rem int64 // spent when forecast was made, forecast p90, actual cost after that point
	Hit               bool
}

type Accuracy struct {
	Scored, Hits int
	Runs         []AccRun
}

// MinScored is how many finished runs are needed before the score is shown.
const MinScored = 5

// Accuracy scores finished runs: the earliest medium-confidence forecast (3+ steps done)
// is a hit when the cost actually spent after it is within its p90. One-sided: it checks
// the upper bound only.
func (l *Ledger) Accuracy() (Accuracy, error) {
	rows, err := l.db.Query(`SELECT id FROM runs WHERE outcome='finished' ORDER BY ended`)
	if err != nil {
		return Accuracy{}, err
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	var a Accuracy
	for _, id := range ids {
		var at, p90 int64
		if l.db.QueryRow(`SELECT spent_micro,p90_micro FROM forecasts WHERE run_id=? AND confidence='medium' ORDER BY id LIMIT 1`, id).Scan(&at, &p90) != nil {
			continue // never forecast with enough steps: not scoreable
		}
		final, _ := l.SpentRun(id)
		r := AccRun{ID: id, AtSpent: at, P90: p90, Rem: final - at, Hit: final-at <= p90}
		a.Scored++
		if r.Hit {
			a.Hits++
		}
		a.Runs = append(a.Runs, r)
	}
	return a, nil
}
