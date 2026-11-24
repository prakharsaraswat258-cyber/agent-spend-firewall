// Package ledger stores metadata only (tokens, cost, model, time). Never prompts or keys.
package ledger

import (
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/forecast"
	_ "modernc.org/sqlite"
)

type Ledger struct{ db *sql.DB }

type Call struct {
	RunID, Provider, Model               string
	Input, Output, CacheRead, CacheWrite int64
	CacheWrite1h                         int64
	CostMicro                            int64
	LatencyMS                            int64
	At                                   time.Time
	// Estimated: the stream was cut off before the provider's final usage event,
	// so some token counts were estimated on the high side.
	Estimated bool
	// Step is the plan step in progress when the call was made ("" if none).
	Step string
}

func Open(path string) (*Ledger, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // sqlite: one writer
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("ledger %s: %w", path, err)
	}
	return &Ledger{db}, nil
}

func (l *Ledger) Close() error { return l.db.Close() }

func (l *Ledger) Record(c Call) error {
	_, err := l.db.Exec(`INSERT OR IGNORE INTO runs(id,started) VALUES(?,?)`, c.RunID, c.At.Unix())
	if err != nil {
		return err
	}
	_, err = l.db.Exec(`INSERT INTO calls(run_id,provider,model,input,output,cache_read,cache_write,cache_write_1h,cost_micro,latency_ms,at,estimated,step)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, c.RunID, c.Provider, c.Model, c.Input, c.Output, c.CacheRead, c.CacheWrite, c.CacheWrite1h,
		c.CostMicro, c.LatencyMS, c.At.Unix(), c.Estimated, c.Step)
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
func (l *Ledger) SaveForecast(run string, spent int64, f forecast.Forecast, done, total int) error {
	method := f.Method
	if method == "" {
		method = "plan"
	}
	_, err := l.db.Exec(`INSERT INTO forecasts(run_id,at,spent_micro,p10_micro,p50_micro,p90_micro,confidence,steps_done,steps_total,method) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		run, time.Now().Unix(), spent, f.P10, f.P50, f.P90, f.Confidence, done, total, method)
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
	CacheWrite1h                         int64
	CostMicro                            int64
	Estimated                            bool
}

// Calls returns the newest calls of a run (metadata only).
func (l *Ledger) Calls(run string, limit int) ([]CallRow, error) {
	rows, err := l.db.Query(`SELECT at,model,input,output,cache_read,cache_write,cache_write_1h,cost_micro,estimated FROM calls WHERE run_id=? ORDER BY id DESC LIMIT ?`, run, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CallRow
	for rows.Next() {
		var c CallRow
		if err := rows.Scan(&c.At, &c.Model, &c.Input, &c.Output, &c.CacheRead, &c.CacheWrite, &c.CacheWrite1h, &c.CostMicro, &c.Estimated); err != nil {
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
	ID                          string
	AtSpent, P10, P50, P90, Rem int64 // spent when forecast was made, its range, actual cost after that point
	Hit                         bool  // P10 <= Rem <= P90
	Under, Over                 bool  // missed: cost below the range, or above it
}

type Accuracy struct {
	Scored, Hits int
	Under, Over  int // misses by direction, so a range that is always off one way is visible
	Runs         []AccRun
}

// MinScored is how many finished runs are needed before the score is shown.
const MinScored = forecast.MinRuns

// Accuracy scores finished runs: the earliest medium-confidence forecast (3+ steps done)
// is right when the cost actually spent after it lands inside the range it showed
// (P10 to P90). Two-sided: a forecast far too high is a miss too.
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
		var r AccRun
		if l.db.QueryRow(`SELECT spent_micro,COALESCE(p10_micro,0),p50_micro,p90_micro FROM forecasts WHERE run_id=? AND confidence='medium' AND COALESCE(method,'plan')='plan' ORDER BY id LIMIT 1`, id).
			Scan(&r.AtSpent, &r.P10, &r.P50, &r.P90) != nil {
			continue // never forecast with enough steps: not scoreable
		}
		if r.P10 == 0 {
			r.P10 = r.P50 / 2 // saved before ranges had a lower end: the old default band
		}
		final, _ := l.SpentRun(id)
		r.ID, r.Rem = id, final-r.AtSpent
		r.Under, r.Over = r.Rem < r.P10, r.Rem > r.P90
		r.Hit = !r.Under && !r.Over
		a.Scored++
		switch {
		case r.Hit:
			a.Hits++
		case r.Under:
			a.Under++
		default:
			a.Over++
		}
		a.Runs = append(a.Runs, r)
	}
	return a, nil
}

// Band is the forecast range calibrated from this machine's finished runs
// (the default band until MinScored runs are scored).
func (l *Ledger) Band() forecast.Band {
	a, err := l.Accuracy()
	if err != nil {
		return forecast.DefaultBand
	}
	var ratios []float64
	for _, r := range a.Runs {
		if r.P50 > 0 {
			ratios = append(ratios, float64(r.Rem)/float64(r.P50))
		}
	}
	return forecast.Calibrate(ratios)
}

// RunBudget is a budget a human raised for this run; false means use the default cap.
func (l *Ledger) RunBudget(run string) (int64, bool) {
	var b sql.NullInt64
	if l.db.QueryRow(`SELECT budget_micro FROM runs WHERE id=?`, run).Scan(&b) != nil || !b.Valid {
		return 0, false
	}
	return b.Int64, true
}

func (l *Ledger) SetRunBudget(run string, micro int64) error {
	if _, err := l.db.Exec(`INSERT OR IGNORE INTO runs(id,started) VALUES(?,?)`, run, time.Now().Unix()); err != nil {
		return err
	}
	_, err := l.db.Exec(`UPDATE runs SET budget_micro=? WHERE id=?`, micro, run)
	return err
}

// SetStopped marks a run stopped by a human answer or a timeout. The proxy refuses
// its calls until `pitstop resume` clears it, so a late "yes" can't restart spending.
func (l *Ledger) SetStopped(run string, stopped bool) error {
	if _, err := l.db.Exec(`INSERT OR IGNORE INTO runs(id,started) VALUES(?,?)`, run, time.Now().Unix()); err != nil {
		return err
	}
	_, err := l.db.Exec(`UPDATE runs SET stopped=? WHERE id=?`, stopped, run)
	return err
}

// Stopped reports whether a run is stopped. A read error counts as stopped (fail closed).
func (l *Ledger) Stopped(run string) (bool, error) {
	var s int
	err := l.db.QueryRow(`SELECT COALESCE(stopped,0) FROM runs WHERE id=?`, run).Scan(&s)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return s == 1 || err != nil, err
}

// SetSetting stores an integer setting, e.g. the caps `pitstop serve` runs with.
func (l *Ledger) SetSetting(key string, v int64) error {
	_, err := l.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, v)
	return err
}

// Setting returns a stored integer setting.
func (l *Ledger) Setting(key string) (int64, bool) {
	var v int64
	if l.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v) != nil {
		return 0, false
	}
	return v, true
}

// StepRow is one plan step as stored (name and status only).
type StepRow struct{ Text, Status string }

// PlanStep is a stored step with the money spent while it was in progress.
type PlanStep struct {
	Position     int
	Text, Status string
	CostMicro    int64
	Calls        int64
}

// SetPlan replaces a run's plan snapshot.
func (l *Ledger) SetPlan(run string, steps []StepRow) error {
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM plan_steps WHERE run_id=?`, run); err != nil {
		return err
	}
	for i, s := range steps {
		if _, err := tx.Exec(`INSERT INTO plan_steps(run_id,position,text,status) VALUES(?,?,?,?)`, run, i, s.Text, s.Status); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Plan returns the run's steps in order with the cost of the calls made while
// each was in progress. Calls made with no step in progress are not shown.
func (l *Ledger) Plan(run string) ([]PlanStep, error) {
	rows, err := l.db.Query(`SELECT p.position, p.text, p.status,
 COALESCE((SELECT SUM(c.cost_micro) FROM calls c WHERE c.run_id=p.run_id AND c.step=p.text),0),
 (SELECT COUNT(*) FROM calls c WHERE c.run_id=p.run_id AND c.step=p.text)
FROM plan_steps p WHERE p.run_id=? ORDER BY p.position`, run)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlanStep
	for rows.Next() {
		var s PlanStep
		if err := rows.Scan(&s.Position, &s.Text, &s.Status, &s.CostMicro, &s.Calls); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ActiveSpan is the time between a run's first and last call (0 with under 2 calls).
func (l *Ledger) ActiveSpan(run string) (time.Duration, error) {
	var lo, hi sql.NullInt64
	err := l.db.QueryRow(`SELECT MIN(at), MAX(at) FROM calls WHERE run_id=?`, run).Scan(&lo, &hi)
	if err != nil || !lo.Valid {
		return 0, err
	}
	return time.Duration(hi.Int64-lo.Int64) * time.Second, nil
}

// DefaultRunLength is the assumed run length until 3 finished runs exist.
const DefaultRunLength = 20 * time.Minute

// TypicalRunLength is the median active span of finished runs (DefaultRunLength
// until there are 3 of them).
func (l *Ledger) TypicalRunLength() time.Duration {
	rows, err := l.db.Query(`SELECT MAX(c.at)-MIN(c.at) FROM runs r JOIN calls c ON c.run_id=r.id
WHERE r.ended IS NOT NULL GROUP BY r.id HAVING COUNT(c.id) >= 2`)
	if err != nil {
		return DefaultRunLength
	}
	defer rows.Close()
	var spans []int64
	for rows.Next() {
		var s int64
		if rows.Scan(&s) == nil && s > 0 {
			spans = append(spans, s)
		}
	}
	if len(spans) < 3 {
		return DefaultRunLength
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i] < spans[j] })
	return time.Duration(spans[len(spans)/2]) * time.Second
}
