package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/price"
)

func tempHome(t *testing.T) string {
	h := t.TempDir()
	t.Setenv("USERPROFILE", h)
	t.Setenv("HOME", h)
	return h
}

// served returns a fake price server holding the built-in table with edits.
func served(t *testing.T, version string, edit func(m map[string]price.Entry)) *httptest.Server {
	tb := price.Default()
	m := map[string]price.Entry{}
	for k, v := range tb.Models {
		m[k] = v
	}
	edit(m)
	b, _ := json.Marshal(price.Table{Version: version, Models: m})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(b) }))
	t.Cleanup(s.Close)
	return s
}

func TestPricesUpdate(t *testing.T) {
	tempHome(t)
	ctx := context.Background()
	var out strings.Builder

	// Older than the built-in table: nothing saved.
	old := served(t, "2026-01-01", func(map[string]price.Entry) {})
	if err := updatePrices(ctx, Config{PricesURL: old.URL}, false, &out); err != nil || !strings.Contains(out.String(), "up to date") {
		t.Fatalf("%v %s", err, out.String())
	}
	if _, err := os.Stat(downloadedPricesPath()); err == nil {
		t.Fatal("an older table must not be saved")
	}

	// Newer with a new model: saved and used.
	nw := served(t, "2099-01-01", func(m map[string]price.Entry) { m["claude-new-9"] = price.Entry{Input: 3, Output: 15} })
	out.Reset()
	if err := updatePrices(ctx, Config{PricesURL: nw.URL}, false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "+ claude-new-9") {
		t.Fatal(out.String())
	}
	tb, source, _ := loadPrices()
	if _, err := tb.Lookup("claude-new-9"); err != nil || source != "downloaded" {
		t.Fatalf("downloaded table not used: %v %s", err, source)
	}

	// Invalid table: refused.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"2100-01-01","models":{"x":{"input":0,"output":1}}}`))
	}))
	defer bad.Close()
	if err := updatePrices(ctx, Config{PricesURL: bad.URL}, false, &out); err == nil {
		t.Fatal("invalid table accepted")
	}

	// Automatic update refuses a big price cut; a person can accept it by hand.
	cut := served(t, "2100-01-01", func(m map[string]price.Entry) { m["claude-opus-5-5"] = price.Entry{Input: 0.01, Output: 0.01} })
	if err := updatePrices(ctx, Config{PricesURL: cut.URL}, true, &out); err == nil || !strings.Contains(err.Error(), "drop by half") {
		t.Fatalf("auto update applied a big drop: %v", err)
	}
	if tb, _, _ := loadPrices(); tb.Version != "2099-01-01" {
		t.Fatalf("table changed to %s", tb.Version)
	}
	if err := updatePrices(ctx, Config{PricesURL: cut.URL}, false, &out); err != nil {
		t.Fatal(err)
	}
}

func TestPricesAddAndCorruptFiles(t *testing.T) {
	tempHome(t)
	if c := pricesAdd([]string{"my-model", "--input", "$1.5", "--output", "6"}); c != 0 {
		t.Fatalf("exit %d", c)
	}
	if c := pricesAdd([]string{"free-model", "--input", "0", "--output", "1"}); c == 0 {
		t.Fatal("a zero input price must be refused")
	}
	tb, source, warn := loadPrices()
	e, err := tb.Lookup("my-model")
	if err != nil || e.Input != 1.5 || e.Output != 6 || !strings.Contains(source, "1 custom") || len(warn) != 0 {
		t.Fatalf("%+v %v %s %v", e, err, source, warn)
	}
	// A corrupt downloaded file is ignored with a warning; the built-in table stays.
	os.WriteFile(downloadedPricesPath(), []byte("{nope"), 0o600)
	if _, source, warn := loadPrices(); !strings.HasPrefix(source, "built-in") || len(warn) != 1 {
		t.Fatalf("%s %v", source, warn)
	}
}

func TestDoctor(t *testing.T) {
	home := tempHome(t)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer proxy.Close()
	cfg := Config{Addr: strings.TrimPrefix(proxy.URL, "http://"), DB: filepath.Join(home, ".pitstop", "ledger.db")}
	none := func(string) (string, error) { return "", errors.New("not found") }
	byName := map[string]check{}
	for _, c := range doctorChecks(cfg, none, func(string) string { return "" }) {
		if _, seen := byName[c.name]; !seen {
			byName[c.name] = c
		}
		if c.status == "FAIL" {
			t.Errorf("unexpected FAIL: %+v", c)
		}
	}
	for _, n := range []string{"ledger", "proxy", "prices", "claude code", "pause hook", "notifications"} {
		if _, ok := byName[n]; !ok {
			t.Errorf("missing check %q", n)
		}
	}
	if byName["claude code"].status != "warn" {
		t.Error("missing claude must warn")
	}
	// Proxy down and a LAN address: both fail with a fix.
	proxy.Close()
	cfg.Addr = "0.0.0.0:4546"
	fails := 0
	for _, c := range doctorChecks(cfg, none, func(string) string { return "" }) {
		if c.status == "FAIL" {
			fails++
			if c.fix == "" {
				t.Errorf("FAIL without a fix: %+v", c)
			}
		}
	}
	if fails != 2 {
		t.Fatalf("want 2 FAILs (address, proxy), got %d", fails)
	}
}
