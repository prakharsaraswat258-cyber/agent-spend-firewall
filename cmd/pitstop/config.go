package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type Config struct {
	Addr      string  `json:"addr"`
	DB        string  `json:"db"`
	BudgetUSD float64 `json:"default_budget_usd"`
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
	c := Config{Addr: "127.0.0.1:4546", DB: filepath.Join(h, ".pitstop", "ledger.db"), BudgetUSD: 5}
	if b, err := os.ReadFile(cfgPath()); err == nil {
		json.Unmarshal(b, &c)
	}
	return c
}

// initCmd saves config and shows how to launch each detected agent. It edits no agent config.
func initCmd(args []string) int {
	c := loadConfig()
	if len(args) == 2 && args[0] == "--budget" {
		fmt.Sscanf(args[1], "%f", &c.BudgetUSD)
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
	fmt.Printf("saved %s (default budget per run $%.2f)\n", cfgPath(), c.BudgetUSD)
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
