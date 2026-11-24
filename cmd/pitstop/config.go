package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/ledger"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/money"
)

type Config struct {
	Addr   string       `json:"addr"`
	DB     string       `json:"db"`
	Budget money.Amount `json:"default_budget_usd"` // micro-dollars; written in dollars
	// PricesURL is where `pitstop prices update` reads the table (default: the project's GitHub).
	PricesURL string `json:"prices_url,omitempty"`
	// AutoPriceUpdate: serve checks for newer prices once a day unless this is false.
	AutoPriceUpdate *bool `json:"auto_price_update,omitempty"`
}

func cfgPath() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".pitstop", "config.json")
}

func loadConfig() Config {
	h, _ := os.UserHomeDir()
	c := Config{Addr: "127.0.0.1:4546", DB: filepath.Join(h, ".pitstop", "ledger.db"), Budget: 5 * money.Micro}
	if b, err := os.ReadFile(cfgPath()); err == nil {
		if err := json.Unmarshal(b, &c); err != nil {
			fmt.Fprintf(os.Stderr, "pitstop: ignoring %s: %v\n", cfgPath(), err)
		}
	}
	return c
}

// initCmd saves config and shows how to launch each detected agent. It edits no agent config.
func initCmd(args []string) int {
	c := loadConfig()
	if len(args) == 2 && args[0] == "--budget" {
		v, err := money.ParseUSD(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "--budget:", err)
			return 2
		}
		c.Budget = money.Amount(v)
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath()), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(cfgPath(), b, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("saved %s (default budget per run %s)\n", cfgPath(), money.Format(int64(c.Budget)))
	found := false
	for _, a := range []string{"claude", "codex", "aider"} {
		if p, err := exec.LookPath(a); err == nil {
			found = true
			fmt.Printf("found %-6s %s\n  run it metered: pitstop run -- %s\n", a, p, a)
		}
	}
	if !found {
		fmt.Println("no agent found. For any tool with a base-URL setting: pitstop run -- <cmd>")
	}
	fmt.Println("start the proxy first: pitstop serve")
	return 0
}

// openLedger opens the ledger, creating the Pitstop folder on first use, so a
// command run before `pitstop serve` works instead of failing on a missing folder.
func openLedger(cfg Config) (*ledger.Ledger, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.DB), 0o700); err != nil {
		return nil, err
	}
	return ledger.Open(cfg.DB)
}

// localAddr refuses any listen address that isn't this machine: the proxy carries
// API keys and the control API answers decision cards.
func localAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%q is not host:port", addr)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("%q is not a loopback address: Pitstop only listens on this machine (use 127.0.0.1:<port>)", addr)
}
