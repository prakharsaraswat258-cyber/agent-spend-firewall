# Landing-page research, 24 Nov 2026

101 landing pages read (124 requested; the rest failed on rate limits, redirects to other companies, 403s or JavaScript-only shells). Groups: LLM gateways and LLM ops (Helicone, Portkey, LiteLLM, OpenRouter, Langfuse, LangSmith, Braintrust, AgentOps, Arize Phoenix, PromptLayer, Lunary, Traceloop, Not Diamond, Weave and others), usage billing and FinOps (OpenMeter, Lago, Orb, Metronome, Stigg, Vantage, CloudZero, Finout, Infracost), coding agents (Cursor, Claude Code, Codex, Aider, Cline, Amp, Zed, Warp, Devin, Replit, Bolt, Augment, CodeRabbit, Greptile, Graphite), dev infrastructure (Vercel, Linear, Resend, Supabase, Neon, PlanetScale, Railway, Fly.io, Render, Clerk, Stripe, Raycast, Tailscale, Temporal, Inngest, Trigger.dev, Convex, Turso, Upstash, Replicate, Baseten, Together, Fireworks, Groq, Cerebras, E2B, Browserbase), open-source tools (Bun, Deno, Astro, Tauri, Biome, Daytona, Hono, Charm), security and quality (Codecov, Snyk, Semgrep, Doppler, Infisical, Kong AI Gateway, Cloudflare AI Gateway), and money or product brands (Mercury, Ramp, Brex, Mistral, Cohere, Sanity, Fathom, Plausible, Cal.com, Prisma, Drizzle, Dub, n8n, Retool).

## What most of them do

| Pattern | How common | Used on Pitstop |
| --- | --- | --- |
| Short outcome headline, one "start" action, docs or GitHub as the second | nearly all | already; GitHub button now shows the live star count |
| Copyable install command in the first screen, with a copy button and tabs per method | most open-source dev tools (Bun, Deno, Astro, Tauri, Biome, Cline, Daytona, Sanity) | added: tabs for `go install` and build-from-source, copy button |
| "Or let your agent do it": a copy-paste setup prompt for coding agents | fast-growing (Deno, Trigger.dev, Convex, Upstash, Stigg, Drizzle, Arize) | added |
| Machine-readable twin of the site (`llms.txt`, `.md` pages) | very common in 2026 (OpenRouter, Langfuse, Resend, Hono, Turso, E2B, Augment) | added `llms.txt` |
| A numbers band | about half (Stripe, Fly, Temporal, Fathom) | added, with product facts only, no invented usage numbers |
| Dedicated security or privacy block | developer-security and enterprise pages (Inngest, Deno, Daytona, Fathom, Cohere, Mistral) | added "What Pitstop never sees", with a masked-key visual (Daytona's idea) |
| An interactive calculator | rare but memorable (Not Diamond savings sliders, Upstash cost card) | added a loop-cost calculator that uses the real price table |
| Comparison table against the alternative | Bun vs Node, Prisma before/after, Kong without/with | added Pitstop vs a plain cap vs a provider spend limit |
| Logo walls, testimonials, user counts | nearly all | not used: Pitstop has no customers yet, and proof is never invented |
| Pricing table on the landing page | rare (Plausible, Cal.com, Codex, LiteLLM, AgentOps) | kept the single $0 figure |
| Multi-column link footer, status link | nearly all | kept |

## Left alone

The scroll-zoom TradingView chart section, with its story and smooth zoom, was directed by the owner and is unchanged.
