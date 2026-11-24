// pitstop serve: local spend-firewall proxy for Anthropic and OpenAI-style APIs.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/caps"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/decide"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/ledger"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/money"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/price"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/proxy"
)

func main() {
	cfg := loadConfig()
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		serve(cfg)
	case "init":
		os.Exit(initCmd(os.Args[2:]))
	case "run":
		os.Exit(runCmd(cfg, os.Args[2:]))
	case "checkpoint":
		os.Exit(checkpointCmd(cfg, os.Args[2:]))
	case "resume":
		os.Exit(resumeCmd(cfg, os.Args[2:]))
	case "status":
		os.Exit(statusCmd(cfg, os.Args[2:]))
	case "hook":
		os.Exit(hookCmd(cfg, os.Args[2:]))
	case "doctor":
		os.Exit(doctorCmd(cfg))
	case "prices":
		os.Exit(pricesCmd(cfg, os.Args[2:]))
	case "notify":
		os.Exit(notifyCmd(cfg, os.Args[2:]))
	case "decide":
		os.Exit(decideCmd(cfg))
	case "accuracy":
		os.Exit(accuracyCmd(cfg))
	case "runs":
		os.Exit(runsCmd(cfg))
	default:
		fmt.Fprintln(os.Stderr, "usage: pitstop init [--budget USD] | serve [flags] | run -- <cmd> | runs | checkpoint <run> | resume <run> | status [run] | decide | accuracy | notify ... | prices ... | doctor")
		os.Exit(2)
	}
}

func serve(cfg Config) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", cfg.Addr, "listen address (keep it local)")
	db := fs.String("db", cfg.DB, "ledger path")
	prices := fs.String("prices", "", "price table JSON (default: built-in)")
	runCap, dayCap, monthCap := &money.Flag{V: int64(cfg.Budget)}, &money.Flag{}, &money.Flag{}
	fs.Var(runCap, "run-cap", "per-run cap, USD (0 = off)")
	fs.Var(dayCap, "day-cap", "per-day cap, USD (0 = off)")
	fs.Var(monthCap, "month-cap", "per-month cap, USD (0 = off)")
	anthUp := fs.String("anthropic-upstream", "https://api.anthropic.com", "where /v1/messages goes (e.g. a company gateway)")
	openUp := fs.String("openai-upstream", "https://api.openai.com", "where /v1/chat/completions and /v1/responses go")
	to := fs.Duration("pause-timeout", 9*time.Minute, "no answer to a pause card in this time: stop and save (keep it under the SDK's 10 minute request timeout)")
	fs.Parse(os.Args[2:])
	if err := localAddr(*addr); err != nil {
		log.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(*db), 0o700); err != nil {
		log.Fatal(err)
	}
	l, err := ledger.Open(*db)
	if err != nil {
		log.Fatal(err)
	}
	var tb *price.Table
	if *prices != "" {
		b, err := os.ReadFile(*prices)
		if err == nil {
			tb, err = price.Load(b)
		}
		if err != nil {
			log.Fatal(err)
		}
	} else {
		autoUpdatePrices(cfg, l) // at most daily; never applies big price drops on its own
		var warn []string
		var source string
		tb, source, warn = loadPrices()
		for _, w := range warn {
			log.Printf("pitstop: warning: %s", w)
		}
		log.Printf("pitstop: prices %s (%s)", tb.Version, source)
	}
	if tb.Stale(time.Now()) {
		log.Printf("pitstop: warning: price table %s is over 30 days old. Run: pitstop prices update", tb.Version)
	}
	a, err := upstream(*anthUp)
	if err != nil {
		log.Fatalf("--anthropic-upstream: %v", err)
	}
	o, err := upstream(*openUp)
	if err != nil {
		log.Fatalf("--openai-upstream: %v", err)
	}
	provs, err := proxy.LoadProviders(filepath.Join(dataDir(), "providers.json"))
	if err != nil {
		log.Fatal(err) // an invalid providers.json must not be half-used
	}
	for name, pr := range provs {
		log.Printf("pitstop: provider %s at /p/%s/ -> %s", name, name, pr.BaseURL)
	}
	p := &proxy.Proxy{Prices: tb, Ledger: l, Anthropic: a, OpenAI: o, Providers: provs,
		Guard: caps.New(caps.Limits{Run: runCap.V, Day: dayCap.V, Month: monthCap.V}, l)}
	// Saved so `pitstop status` and other commands show the caps actually in force.
	for k, v := range map[string]int64{"run_cap": runCap.V, "day_cap": dayCap.V, "month_cap": monthCap.V} {
		if err := l.SetSetting(k, v); err != nil {
			log.Fatal(err)
		}
	}
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if err := p.Broken(); err != nil {
			http.Error(w, "ledger write failed: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})
	dec := decide.New(*to)
	hub := setupNotify(context.Background(), cfg, *addr, l, dec, p, *to)
	dec.Notify = hub.Card
	dec.Log = func(c decide.Card, a decide.Answer, d time.Duration) {
		hub.Resolved(c, a) // close the card on every channel
		ans := "stop"
		if a.Continue {
			ans = "continue"
		} else if a.TimedOut {
			ans = "timeout"
		}
		l.SaveDecision(ledger.Decision{Run: c.Run, Reason: c.Reason, Answer: ans, Channel: a.Channel, Asked: c.Asked, Spent: c.Spent,
			Budget: c.Budget, P50: c.P50, P90: c.P90, Offered: c.Offer, Added: a.Add, ResponseMS: d.Milliseconds()})
	}
	p.Decider = dec
	log.Printf("pitstop on http://%s (prices %s, run cap %s). Set ANTHROPIC_BASE_URL / OPENAI_BASE_URL to it.", *addr, tb.Version, money.Format(runCap.V))
	http.Handle("/", p)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

// upstream parses an upstream base URL. Keys pass through it, so it must be
// https, except for a gateway on this machine.
func upstream(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("%q is not a full http(s) URL", raw)
	}
	if h := u.Hostname(); u.Scheme == "http" && h != "localhost" && h != "127.0.0.1" && h != "::1" {
		return nil, fmt.Errorf("%q must use https; plain http is only allowed for localhost", raw)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u, nil
}
