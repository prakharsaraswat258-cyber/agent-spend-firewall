# Pitstop (Agent Spend Firewall) — money base

Local proxy that meters LLM cost exactly and enforces hard caps before a call is sent.

```bash
go build -o pitstop ./cmd/pitstop
./pitstop serve --run-cap 5 --day-cap 20
export ANTHROPIC_BASE_URL=http://127.0.0.1:4546   # Claude Code / SDK
export OPENAI_BASE_URL=http://127.0.0.1:4546/v1   # OpenAI-style
```

- Streaming pass-through for `/v1/messages`, `/v1/chat/completions`, `/v1/responses`.
- Cost in integer micro-dollars from `internal/price/prices.json` (verify prices before trusting).
- Each call reserves worst-case cost first; run/day/month caps cannot be jointly overshot. Over cap: HTTP 429.
- Unknown model: HTTP 400 with fix. Keys and prompts are never stored; ledger is metadata only.
- Header `X-Pitstop-Run` groups calls into runs; default is one run per `serve`.

## Commands
- `pitstop init [--budget USD]`: saves `~/.pitstop/config.json`, detects claude/codex/aider. Edits no agent config.
- `pitstop run -- claude`: registers a run (repo + branch auto-detected), points the agent at the proxy via `ANTHROPIC_BASE_URL` / `OPENAI_BASE_URL` with the run id in the URL path.
- `pitstop runs`: recent runs with calls and cost.

- Loop detector: same tool call or same error repeated 3x records an alert (LOOPS column in `pitstop runs`); 5x blocks the call with HTTP 429. Reads the request history, keeps only tool names.

- `pitstop checkpoint <run>`: commits the run's working tree (incl. untracked, minus .gitignored) to `pitstop/wip/<run>`. Your branch, index and files are untouched. Runs automatically on a loop stop or cap block.
- `pitstop resume <run>`: reopens Claude Code on the same run (`claude --resume <session>` if the session id was seen, else `claude --continue`), so spend and caps carry on. Other agents: prints manual steps.

- Progress + forecast: the proxy reads the agent's plan from the request history (Claude Code `TodoWrite`, or a `report_progress` tool call with `{done,total}`). Forecast = steps left x average cost per step x context-growth factor (1-2x); p90 = 2 x p50; "low" confidence until 3 steps are done. Each forecast is saved for later accuracy scoring.
- `pitstop status [run]`: one line, usable as a Claude Code status line: `$3.20 / $5.00 · 3/8 steps · likely $5.33-$10.67 more · on track · medium confidence`. Says "WON'T FIT BUDGET" when spent >= 70% of budget and p50 > what is left. No plan seen means no forecast.

- Decision card: when the forecast says the run won't fit (>= 70% spent and p50 > left), or the run cap is hit, the proxy holds the call and shows progress, spend, likely range and confidence. Answer with `pitstop decide` (second terminal: Enter = Continue +offer, a number = Continue +$N, `s` = Stop & save) or the desktop popup (macOS osascript, Linux zenity). Continue raises that run's budget; Stop checkpoints to the WIP branch. No answer in `--pause-timeout` (default 10m) means stop; it never auto-continues. Day and month caps stay hard: no card, no raise. Every card and answer is logged in the `decisions` table.
- Control API (`/pitstop/pending`, `/pitstop/answer`): loopback Host only and an `X-Pitstop: 1` header required, so web pages can't answer cards.

Unconfirmed: the popup and `decide` against a real agent (tested with a fake upstream only); the held request surviving a 10 minute wait in Claude Code (client timeouts unknown); Claude Code session-id capture and `claude --resume` against a real session.

Not yet: "no code change in N steps" signal, Telegram/phone channel, per-repo budgets, Codex hook check.
Not verified: cost vs provider console (needs 20 real runs), real Claude Code session.

- Dashboard: http://127.0.0.1:4546/dashboard (same port as the proxy). Today/month spend, waiting decision cards with Continue/Stop buttons, runs with step and money bars, click a run for its calls. Static page, no external requests; data comes from `/pitstop/*` with the guard header.

- Accuracy score: `pitstop run` closes the run when the agent exits (finished = plan complete, stopped = plan incomplete, ended = no plan) and prints total cost, whether the forecast held, and loop alerts. `pitstop accuracy` and the dashboard show "right N of M" once 5 finished runs have a forecast. A forecast counts as right when the cost actually spent after the earliest medium-confidence forecast (3+ steps done) is within its p90. Upper bound only: a forecast far too high still counts as right.

## Website
`site/` is the landing page (`index.html`) and a sign-in page (`login.html`). Static files, no build step: `cd site && python3 -m http.server 8765`, then open http://localhost:8765. The sign-in page has no backend yet: nothing typed there is sent or stored. The scroll-zoom chart uses TradingView Lightweight Charts and Lenis from unpkg, and its gain and loss figures are a made-up sample company, not real data.
