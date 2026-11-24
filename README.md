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

Unconfirmed: Claude Code session-id capture and `claude --resume` against a real session.

Not yet: pause card, forecast, checkpoint, "no code change in N steps" signal, dashboard, per-run budgets, Codex hook check.
Not verified: cost vs provider console (needs 20 real runs), real Claude Code session.
