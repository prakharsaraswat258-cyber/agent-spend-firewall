-- Schema as first released (agent-spend-firewall main, November 2026).
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
CREATE INDEX IF NOT EXISTS calls_at ON calls(at);
