# Pitstop (Agent Spend Firewall) — money base

Local proxy that meters LLM cost exactly and enforces hard caps before a call is sent.

```bash
go build -o pitstop ./cmd/pitstop
./pitstop serve --run-cap 5 --day-cap 20
export ANTHROPIC_BASE_URL=http://127.0.0.1:4546   # Claude Code / SDK
export OPENAI_BASE_URL=http://127.0.0.1:4546/v1   # OpenAI-style
```

- Streaming pass-through for `POST /v1/messages`, `/v1/chat/completions`, `/v1/responses`. Free calls (reads, `count_tokens`, `input_tokens`, cancels) pass unmetered. Any other POST (Batch API, embeddings, images, audio, legacy completions) is blocked with a 400, because Pitstop cannot price it yet.
- A response cut off mid-stream (Ctrl-C, dropped connection) is still billed: input as reported, output estimated high from the text streamed, and the call is marked `estimated` in the ledger.
- Anthropic 1-hour cache writes are billed at their own rate (2x input unless `cache_write_1h` is set in the price table).
- If a ledger write fails, every later call is blocked (HTTP 503) until restart, so nothing goes out unmetered.
- Cost in integer micro-dollars from `internal/price/prices.json`, copied from the Anthropic and OpenAI pricing pages on 2026-11-24. Anthropic model IDs follow the `claude-<family>-<major>-<minor>` pattern and were not listed on the page.
- Each call reserves worst-case cost first; run/day/month caps cannot be jointly overshot. Over cap: HTTP 403 (not 429, which SDKs retry). Images and PDFs are reserved at a fixed allowance (6,000 tokens per image or PDF page), not by their encoded size.
- Unknown model: HTTP 400 with fix. Prices match the exact model ID or the ID plus a date suffix (`-20250929`, `-2024-07-18`), never a looser prefix. Long-prompt tiers are priced: Haiku 5.5 over 100K tokens, and OpenAI's over-272K rates. Models with no cached-input price bill cached tokens as normal input.
- Requests to paths Pitstop doesn't meter go to the provider whose credentials they carry (`x-api-key` to Anthropic, `Bearer` to OpenAI); with neither, 404.
- Day and month caps use the machine's local time zone. Keys and prompts are never stored; ledger is metadata only.
- Runs: `pitstop run` puts the run id in the base URL; header `X-Pitstop-Run` names a run for any client. A Claude Code session started without `pitstop run` becomes its own run (`cc-<session>`) with the normal run budget. Calls with no identity at all go to a daily `untagged-YYYYMMDD` run that only the day and month caps limit.

## Commands
- `pitstop init [--budget USD]`: saves `~/.pitstop/config.json`, detects claude/codex/aider. Edits no agent config.
- `pitstop run -- claude`: registers a run (repo + branch auto-detected), points the agent at the proxy via `ANTHROPIC_BASE_URL` / `OPENAI_BASE_URL` with the run id in the URL path. It also sets `CLAUDE_CODE_ENABLE_TODO_TOOLS=1` for the agent (unless you set it), because Claude Code turns its task list off on models newer than Opus 4.7 / Sonnet 4.6 / Haiku 4.5, and without a plan there is no forecast.
- `pitstop runs`: recent runs with calls and cost.

- Loop detector: same tool call or same error repeated 3x records an alert (LOOPS column in `pitstop runs`); 5x blocks the call with HTTP 403. Reads the request history, keeps only tool names. When you type a new message (for example after `pitstop resume`), the count starts again.

- `pitstop checkpoint <run>`: commits the run's working tree (incl. untracked, minus .gitignored) to `pitstop/wip/<run>`. Your branch, index and files are untouched. Runs automatically on a loop stop or cap block.
- `pitstop resume <run>`: reopens Claude Code on the same run (`claude --resume <session>` if the session id was seen, else `claude --continue`), so spend and caps carry on. Other agents: prints manual steps.

- Progress + forecast: the proxy reads the agent's plan from the request history (Claude Code `TaskCreate`/`TaskUpdate` or `TodoWrite`, or a `report_progress` tool call with `{done,total}`). Forecast = steps left x average cost per step x context-growth factor (1-2x); the range shown is p10 to p90, starting at 0.5x to 2x p50 and recalibrated from your own finished runs once 5 are scored; "low" confidence until 3 steps are done. Each forecast is saved for later accuracy scoring.
- `pitstop status [run]`: one line, usable as a Claude Code status line: `$3.20 / $5.00 · 3/8 steps · likely $2.67-$10.67 more · on track · medium confidence`. The budget shown is the one the proxy enforces (a raised budget, else `serve --run-cap`). Says "WON'T FIT BUDGET" when spent >= 70% of budget and p50 > what is left. No plan seen means no forecast.

- Pause at a safe point: `pitstop run -- claude` installs a `PreToolUse` hook for that session only (through `claude --settings`; your settings files are not edited). Before each tool call the hook asks the proxy whether the run may go on; when a card is open it waits for the answer, then lets the tool run or blocks it with the reason. Holding the model request is the fallback for agents without hooks. A stopped run stays stopped (a late "yes" is refused) until `pitstop resume`.
- Decision card: when the forecast says the run won't fit (>= 70% spent and p50 > left), or the run cap is hit, the proxy holds the call and shows progress, spend, likely range and confidence. Answer with `pitstop decide` (second terminal: Enter = Continue +offer, a number = Continue +$N, `s` = Stop & save) or the desktop popup (macOS osascript, Linux zenity). Continue raises that run's budget; Stop checkpoints to the WIP branch. No answer in `--pause-timeout` (default 9m, under the SDK's 10 minute request timeout) means stop; it never auto-continues. Day and month caps stay hard: no card, no raise. Every card and answer is logged in the `decisions` table.
- Control API (`/pitstop/pending`, `/pitstop/answer`): loopback Host only and an `X-Pitstop: 1` header required, so web pages can't answer cards.

Unconfirmed: the popup and `decide` against a real agent (tested with a fake upstream only); the held request surviving a 10 minute wait in Claude Code (client timeouts unknown); Claude Code session-id capture and `claude --resume` against a real session.

Not yet: "no code change in N steps" signal, Telegram/phone channel, per-repo budgets, Codex hook check.
Not verified: cost vs provider console (needs 20 real runs), real Claude Code session.

- Dashboard: http://127.0.0.1:4546/dashboard (same port as the proxy). Today/month spend, waiting decision cards with Continue/Stop buttons, runs with step and money bars, click a run for its calls. Static page, no external requests; data comes from `/pitstop/*` with the guard header.

- Accuracy score: `pitstop run` closes the run when the agent exits (finished = plan complete, stopped = plan incomplete, ended = no plan) and prints total cost, whether the forecast held, and loop alerts. `pitstop accuracy` and the dashboard show "right N of M" once 5 finished runs have a forecast. A forecast counts as right when the cost actually spent after the earliest medium-confidence forecast (3+ steps done) lands inside the range it showed (p10 to p90). Misses are counted both ways (too high, too low).

## Website
`site/` is the landing page (`index.html`) and a sign-in page (`login.html`). Static files, no build step: `cd site && python3 -m http.server 8765`, then open http://localhost:8765. The sign-in page has no backend yet: nothing typed there is sent or stored. The scroll-zoom chart uses TradingView Lightweight Charts from unpkg and native smooth scrolling, and its gain and loss figures are a made-up sample company, not real data.
