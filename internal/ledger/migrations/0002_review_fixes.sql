-- Cut-off streams, 1-hour cache writes, stop state, forecast ranges, plan steps, settings.
ALTER TABLE calls ADD COLUMN cache_write_1h INTEGER NOT NULL DEFAULT 0;
ALTER TABLE calls ADD COLUMN estimated INTEGER NOT NULL DEFAULT 0;
ALTER TABLE calls ADD COLUMN step TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN stopped INTEGER NOT NULL DEFAULT 0;
ALTER TABLE forecasts ADD COLUMN p10_micro INTEGER NOT NULL DEFAULT 0;
ALTER TABLE forecasts ADD COLUMN method TEXT NOT NULL DEFAULT 'plan';
CREATE TABLE IF NOT EXISTS plan_steps(run_id TEXT, position INTEGER, text TEXT, status TEXT, PRIMARY KEY(run_id, position));
CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY, value INTEGER);
