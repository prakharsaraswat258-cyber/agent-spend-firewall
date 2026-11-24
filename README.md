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

Not yet: pause card, forecast, checkpoint, loop detector, dashboard, init command.
Not verified: cost vs provider console (needs 20 real runs), real Claude Code session.
