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
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/caps"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/decide"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/ledger"
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
	runCap := fs.Float64("run-cap", cfg.BudgetUSD, "per-run cap, USD (0 = off)")
	dayCap := fs.Float64("day-cap", 0, "per-day cap, USD (0 = off)")
	monthCap := fs.Float64("month-cap", 0, "per-month cap, USD (0 = off)")
	to := fs.Duration("pause-timeout", 9*time.Minute, "no answer to a pause card in this time: stop and save (keep it under the SDK's 10 minute request timeout)")
	fs.Parse(os.Args[2:])

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
	m := func(usd float64) int64 { return int64(usd * 1e6) }
	a, _ := url.Parse("https://api.anthropic.com")
	o, _ := url.Parse("https://api.openai.com")
	p := &proxy.Proxy{Prices: tb, Ledger: l, Anthropic: a, OpenAI: o,
		Guard: caps.New(caps.Limits{Run: m(*runCap), Day: m(*dayCap), Month: m(*monthCap)}, l)}
	// Saved so `pitstop status` and other commands show the caps actually in force.
	for k, v := range map[string]int64{"run_cap": m(*runCap), "day_cap": m(*dayCap), "month_cap": m(*monthCap)} {
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
	log.Printf("pitstop on http://%s (prices %s, run cap $%.2f). Set ANTHROPIC_BASE_URL / OPENAI_BASE_URL to it.", *addr, tb.Version, *runCap)
	http.Handle("/", p)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
