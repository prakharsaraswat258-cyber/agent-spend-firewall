package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/decide"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/ledger"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/proxy"
)

type check struct {
	status string // "ok", "warn", "FAIL"
	name   string
	detail string
	fix    string
}

// doctorChecks runs every check. lookPath and version are swappable for tests.
func doctorChecks(cfg Config, lookPath func(string) (string, error), version func(string) string) []check {
	var cs []check
	add := func(status, name, detail, fix string) { cs = append(cs, check{status, name, detail, fix}) }

	// Address must stay on this machine.
	if err := localAddr(cfg.Addr); err != nil {
		add("FAIL", "address", err.Error(), `set "addr" in `+cfgPath()+` to 127.0.0.1:4546`)
	}

	// Ledger.
	var l *ledger.Ledger
	if err := os.MkdirAll(filepath.Dir(cfg.DB), 0o700); err != nil {
		add("FAIL", "ledger", err.Error(), "check permissions on "+filepath.Dir(cfg.DB))
	} else if l, err = ledger.Open(cfg.DB); err != nil {
		add("FAIL", "ledger", err.Error(), "check the file "+cfg.DB)
	} else if err := l.SetSetting("doctor_checked_at", time.Now().Unix()); err != nil {
		add("FAIL", "ledger", "not writable: "+err.Error(), "check disk space and permissions on "+cfg.DB)
		l.Close()
		l = nil
	} else {
		add("ok", "ledger", cfg.DB+" is writable", "")
		defer l.Close()
	}

	// Proxy.
	c := &http.Client{Timeout: 2 * time.Second}
	if resp, err := c.Get("http://" + cfg.Addr + "/healthz"); err != nil {
		add("FAIL", "proxy", "not running at "+cfg.Addr, "start it: pitstop serve")
	} else {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode == 200 {
			add("ok", "proxy", "running at "+cfg.Addr, "")
		} else {
			add("FAIL", "proxy", "blocking all calls: "+strings.TrimSpace(string(b)), "fix the cause above, then restart pitstop serve")
		}
	}

	// Prices.
	t, source, warn := loadPrices()
	for _, w := range warn {
		add("warn", "prices", w, "")
	}
	age := ""
	if d, ok := t.Date(); ok {
		age = fmt.Sprintf(", %d days old", int(time.Since(d).Hours()/24))
	}
	if t.Stale(time.Now()) {
		add("warn", "prices", fmt.Sprintf("table %s (%s%s) is over 30 days old", t.Version, source, age), "pitstop prices update")
	} else {
		add("ok", "prices", fmt.Sprintf("table %s (%s%s), %d models", t.Version, source, age, len(t.Models)), "")
	}

	// Caps in force (from the last serve start).
	if l != nil {
		if rc, ok := l.Setting("run_cap"); ok {
			d, _ := l.Setting("day_cap")
			m, _ := l.Setting("month_cap")
			show := func(v int64) string {
				if v == 0 {
					return "off"
				}
				return decide.USD(v)
			}
			add("ok", "caps", fmt.Sprintf("run %s, day %s, month %s", show(rc), show(d), show(m)), "")
			if d == 0 && m == 0 {
				add("warn", "caps", "no day or month cap: calls outside a run are not limited", "pitstop serve --day-cap 20")
			}
		}
	}

	// Agents.
	if p, err := lookPath("claude"); err != nil {
		add("warn", "claude code", "`claude` not found on PATH", "install Claude Code, or use any tool with a base-URL setting via pitstop run")
	} else {
		add("ok", "claude code", strings.TrimSpace(p+" "+version(p)), "")
		add("ok", "claude code", "connect a session with: pitstop run -- claude", "")
	}
	if _, err := lookPath("codex"); err == nil {
		add("ok", "codex", "found; run it metered with the pause hook: pitstop run -- codex (needs OPENAI_API_KEY)", "")
	}
	if _, err := lookPath("aider"); err == nil {
		add("ok", "aider", "found; meter and cap it with: pitstop run -- aider (no pause hook: pauses hold the request)", "")
	}

	// Named providers (/p/<name>/).
	if provs, err := proxy.LoadProviders(filepath.Join(dataDir(), "providers.json")); err != nil {
		add("FAIL", "providers", err.Error(), "fix providers.json; pitstop serve will not start with it")
	} else if len(provs) > 0 {
		names := make([]string, 0, len(provs))
		for n := range provs {
			names = append(names, n)
		}
		sort.Strings(names)
		add("ok", "providers", "/p/<name>/ for "+strings.Join(names, ", "), "")
	}

	// Pause hook (written by pitstop run).
	hp := filepath.Join(dataDir(), "claude-hooks.json")
	if b, err := os.ReadFile(hp); err != nil {
		add("ok", "pause hook", "installed on the first `pitstop run -- claude`", "")
	} else {
		var s struct {
			Hooks struct {
				PreToolUse []struct {
					Hooks []struct {
						Command string `json:"command"`
					} `json:"hooks"`
				} `json:"PreToolUse"`
			} `json:"hooks"`
		}
		exe := ""
		if json.Unmarshal(b, &s) == nil && len(s.Hooks.PreToolUse) > 0 && len(s.Hooks.PreToolUse[0].Hooks) > 0 {
			cmd := s.Hooks.PreToolUse[0].Hooks[0].Command
			if q := strings.Index(cmd[1:], `"`); strings.HasPrefix(cmd, `"`) && q > 0 {
				exe = cmd[1 : q+1]
			}
		}
		if _, err := os.Stat(exe); exe == "" || err != nil {
			add("warn", "pause hook", "points to a pitstop binary that no longer exists", "it is rewritten on the next pitstop run -- claude")
		} else {
			add("ok", "pause hook", hp, "")
		}
	}

	// Notifications.
	if l != nil {
		if channelOn(l, "desktop") {
			add("ok", "notifications", "desktop on", "")
		} else {
			add("warn", "notifications", "desktop off: cards only show in the terminal and dashboard", "pitstop notify enable desktop")
		}
		if channelOn(l, "telegram") {
			_, chat := l.Setting("telegram_chat_id")
			switch {
			case telegramToken() == "":
				add("warn", "notifications", "telegram on, but "+tokenEnv+" is not set", "set it in the environment or "+filepath.Join(dataDir(), ".env"))
			case !chat:
				add("warn", "notifications", "telegram on, but not set up", "pitstop notify telegram setup")
			default:
				add("ok", "notifications", "telegram on", "")
			}
		}
	}
	return cs
}

func doctorCmd(cfg Config) int {
	version := func(p string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, p, "--version").Output()
		if err != nil {
			return ""
		}
		return "(" + strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]) + ")"
	}
	failed := 0
	for _, c := range doctorChecks(cfg, exec.LookPath, version) {
		line := fmt.Sprintf("%-4s  %-13s %s", c.status, c.name, c.detail)
		if c.fix != "" {
			line += "\n      fix: " + c.fix
		}
		fmt.Println(line)
		if c.status == "FAIL" {
			failed++
		}
	}
	if failed > 0 {
		fmt.Printf("\n%d problem(s) need fixing.\n", failed)
		return 1
	}
	fmt.Println("\nAll required checks passed.")
	return 0
}
