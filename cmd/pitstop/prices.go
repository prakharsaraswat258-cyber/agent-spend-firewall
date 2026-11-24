package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/ledger"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/price"
)

// DefaultPricesURL is where `pitstop prices update` reads the maintained table.
const DefaultPricesURL = "https://raw.githubusercontent.com/ShadowIzzHerZ/agent-spend-firewall/main/internal/price/prices.json"

func dataDir() string              { return filepath.Dir(cfgPath()) }
func downloadedPricesPath() string { return filepath.Join(dataDir(), "prices.json") }
func customPricesPath() string     { return filepath.Join(dataDir(), "prices-custom.json") }

func pricesURL(cfg Config) string {
	if cfg.PricesURL != "" {
		return cfg.PricesURL
	}
	return DefaultPricesURL
}

// baseTable is the newest of the built-in table and the last downloaded one.
func baseTable() (t *price.Table, source string, warn []string) {
	t, source = price.Default(), "built-in"
	b, err := os.ReadFile(downloadedPricesPath())
	if err != nil {
		return t, source, nil
	}
	dl, err := price.Load(b)
	switch {
	case err != nil:
		warn = append(warn, fmt.Sprintf("ignored %s: %v (run pitstop prices update)", downloadedPricesPath(), err))
	case price.Newer(dl.Version, t.Version):
		t, source = dl, "downloaded"
	}
	return t, source, warn
}

func loadCustom() (map[string]price.Entry, error) {
	b, err := os.ReadFile(customPricesPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]price.Entry
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", customPricesPath(), err)
	}
	return m, nil
}

// loadPrices is the table `serve` uses: base table plus valid custom entries.
func loadPrices() (t *price.Table, source string, warn []string) {
	t, source, warn = baseTable()
	custom, err := loadCustom()
	if err != nil {
		return t, source, append(warn, "ignored your custom prices: "+err.Error())
	}
	ok := map[string]price.Entry{}
	for name, e := range custom {
		if err := (&price.Table{Version: t.Version, Models: map[string]price.Entry{name: e}}).Validate(); err != nil {
			warn = append(warn, fmt.Sprintf("ignored custom price for %s: %v", name, err))
			continue
		}
		ok[name] = e
	}
	if len(ok) > 0 {
		source += fmt.Sprintf(" + %d custom", len(ok))
	}
	return t.WithCustom(ok), source, warn
}

// fetchPrices downloads and validates the maintained table.
func fetchPrices(ctx context.Context, url string) (*price.Table, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, nil, err
	}
	t, err := price.Load(b)
	if err != nil {
		return nil, nil, fmt.Errorf("downloaded table is not valid: %w", err)
	}
	return t, b, nil
}

// updatePrices fetches the table and saves it when it is newer. auto (from serve)
// refuses tables that cut a known model's price by half or more: a bad or tampered
// file must not quietly switch the caps off. A person can still accept it by hand.
func updatePrices(ctx context.Context, cfg Config, auto bool, out io.Writer) error {
	cur, _, _ := baseTable()
	nt, raw, err := fetchPrices(ctx, pricesURL(cfg))
	if err != nil {
		return err
	}
	if !price.Newer(nt.Version, cur.Version) {
		fmt.Fprintf(out, "prices are up to date (version %s)\n", cur.Version)
		return nil
	}
	cs := price.Diff(cur, nt)
	fmt.Fprintf(out, "price table %s -> %s: %s\n", cur.Version, nt.Version, price.Summary(cs))
	big := 0
	for _, c := range cs {
		fmt.Fprintln(out, "  "+c.String())
		if c.BigDropPct >= 50 {
			big++
		}
	}
	if auto && big > 0 {
		return fmt.Errorf("not applied automatically: %d price(s) drop by half or more. Review them with: pitstop prices update", big)
	}
	if err := os.MkdirAll(dataDir(), 0o700); err != nil {
		return err
	}
	tmp := downloadedPricesPath() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, downloadedPricesPath()); err != nil {
		return err
	}
	fmt.Fprintln(out, "saved. Restart pitstop serve to use it.")
	return nil
}

// autoUpdatePrices runs at serve start, at most once a day, unless turned off.
func autoUpdatePrices(cfg Config, l *ledger.Ledger) {
	if cfg.AutoPriceUpdate != nil && !*cfg.AutoPriceUpdate {
		return
	}
	if last, ok := l.Setting("prices_checked_at"); ok && time.Since(time.Unix(last, 0)) < 24*time.Hour {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var b strings.Builder
	err := updatePrices(ctx, cfg, true, &b)
	_ = l.SetSetting("prices_checked_at", time.Now().Unix())
	if err != nil {
		fmt.Fprintf(os.Stderr, "pitstop: price check skipped: %v\n", err)
		return
	}
	if s := strings.TrimSpace(b.String()); !strings.HasPrefix(s, "prices are up to date") {
		fmt.Fprintln(os.Stderr, "pitstop: "+s)
	}
}

// pricesCmd: pitstop prices [show <model>] | update | add <model> --input X --output Y ...
func pricesCmd(cfg Config, args []string) int {
	if len(args) == 0 || args[0] == "show" {
		return pricesShow(args)
	}
	switch args[0] {
	case "update":
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := updatePrices(ctx, cfg, false, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "price update failed:", err)
			return 1
		}
		return 0
	case "add":
		return pricesAdd(args[1:])
	}
	fmt.Fprintln(os.Stderr, "usage: pitstop prices [show <model>] | prices update | prices add <model> --input USD --output USD [--cache-read USD] [--cache-write USD] [--cache-write-1h USD]   (USD per million tokens)")
	return 2
}

func pricesShow(args []string) int {
	t, source, warn := loadPrices()
	for _, w := range warn {
		fmt.Fprintln(os.Stderr, "warning: "+w)
	}
	if len(args) == 2 {
		e, err := t.Lookup(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		b, _ := json.MarshalIndent(e, "", "  ")
		fmt.Printf("%s (USD per million tokens):\n%s\n", args[1], b)
		return 0
	}
	age := "unknown"
	if d, ok := t.Date(); ok {
		age = fmt.Sprintf("%d days old", int(time.Since(d).Hours()/24))
	}
	fmt.Printf("price table %s (%s, %s), %d models\n", t.Version, source, age, len(t.Models))
	if t.Stale(time.Now()) {
		fmt.Println("warning: prices are over 30 days old. Run: pitstop prices update")
	}
	names := make([]string, 0, len(t.Models))
	for n := range t.Models {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		e := t.Models[n]
		fmt.Printf("  %-20s $%g in  $%g out\n", n, e.Input, e.Output)
	}
	return 0
}

func pricesAdd(args []string) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(os.Stderr, "usage: pitstop prices add <model> --input USD --output USD [--cache-read USD] [--cache-write USD] [--cache-write-1h USD]")
		return 2
	}
	model := args[0]
	fs := flag.NewFlagSet("prices add", flag.ContinueOnError)
	var in, out, cr, cw, cw1h string
	fs.StringVar(&in, "input", "", "USD per million input tokens")
	fs.StringVar(&out, "output", "", "USD per million output tokens")
	fs.StringVar(&cr, "cache-read", "", "USD per million cached input tokens")
	fs.StringVar(&cw, "cache-write", "", "USD per million 5-minute cache write tokens")
	fs.StringVar(&cw1h, "cache-write-1h", "", "USD per million 1-hour cache write tokens")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	num := func(name, s string) (float64, error) {
		if s == "" {
			return 0, nil
		}
		v, err := strconv.ParseFloat(strings.TrimPrefix(s, "$"), 64)
		if err != nil || v < 0 {
			return 0, fmt.Errorf("--%s %q is not a price", name, s)
		}
		return v, nil
	}
	var e price.Entry
	var err error
	for _, f := range []struct {
		name, s string
		dst     *float64
	}{{"input", in, &e.Input}, {"output", out, &e.Output}, {"cache-read", cr, &e.CacheRead}, {"cache-write", cw, &e.CacheWrite}, {"cache-write-1h", cw1h, &e.CacheWrite1h}} {
		if *f.dst, err = num(f.name, f.s); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}
	if err := (&price.Table{Version: "2000-01-01", Models: map[string]price.Entry{model: e}}).Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "not saved:", err, "(use the provider's published price; Pitstop never guesses one)")
		return 2
	}
	custom, err := loadCustom()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if custom == nil {
		custom = map[string]price.Entry{}
	}
	custom[model] = e
	b, _ := json.MarshalIndent(custom, "", "  ")
	if err := os.MkdirAll(dataDir(), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.WriteFile(customPricesPath(), b, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("saved %s: $%g in / $%g out per million tokens (in %s). Restart pitstop serve to use it.\n", model, e.Input, e.Output, customPricesPath())
	return 0
}
