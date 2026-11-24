package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/ledger"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/money"
)

func gitOut(dir string, args ...string) string {
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := c.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// detect returns repo name and branch of dir; empty when not a git repo.
func detect(dir string) (repo, branch, top string) {
	top = gitOut(dir, "rev-parse", "--show-toplevel")
	if top == "" {
		return "", "", ""
	}
	return filepath.Base(top), gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD"), top
}

func newID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return time.Now().Format("0102-1504") + "-" + hex.EncodeToString(b)
}

// runCmd: pitstop run -- <agent cmd...>. Registers a run, points the agent at the proxy.
func runCmd(cfg Config, args []string) int {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: pitstop run -- <command> [args]  (e.g. pitstop run -- claude)")
		return 2
	}
	resp, err := http.Get("http://" + cfg.Addr + "/healthz")
	if err != nil {
		fmt.Fprintf(os.Stderr, "pitstop proxy not running at %s. Start it: pitstop serve\n", cfg.Addr)
		return 1
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		fmt.Fprintf(os.Stderr, "pitstop proxy at %s is blocking all calls. Run: pitstop doctor\n", cfg.Addr)
		return 1
	}
	resp.Body.Close()
	l, err := openLedger(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	cwd, _ := os.Getwd()
	repo, branch, top := detect(cwd)
	id := newID()
	if err := l.StartRun(ledger.Run{ID: id, Repo: repo, Branch: branch, Agent: agentName(args[0]), Dir: top, Started: time.Now()}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	l.Close()
	fmt.Fprintf(os.Stderr, "pitstop run %s  repo=%s branch=%s\n", id, repo, branch)
	return launch(cfg, id, "", args)
}

// agentName: "claude" for claude, claude.exe, /usr/bin/claude.
func agentName(cmd string) string {
	return strings.TrimSuffix(strings.ToLower(filepath.Base(cmd)), ".exe")
}

// launch runs cmd with the proxy env for run id, in dir (or here when empty).
func launch(cfg Config, id, dir string, args []string) int {
	base := "http://" + cfg.Addr + "/run/" + id
	if agentName(args[0]) == "codex" {
		if exe, err := os.Executable(); err == nil {
			args = append(append([]string{args[0]}, codexArgs(base, exe)...), args[1:]...)
			codexNotes()
		}
	}
	if agentName(args[0]) == "claude" {
		// Pause at a safe point (before a tool call) instead of holding an HTTP request.
		if path, err := claudeHookSettings(); err == nil {
			args = append([]string{args[0], "--settings", path}, args[1:]...)
		} else {
			fmt.Fprintf(os.Stderr, "pitstop: could not install the pause hook (%v); pauses fall back to holding the request\n", err)
		}
	}
	c := exec.Command(args[0], args[1:]...)
	c.Dir = dir
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	c.Env = append(os.Environ(), "ANTHROPIC_BASE_URL="+base, "OPENAI_BASE_URL="+base+"/v1", "PITSTOP_RUN="+id)
	// Claude Code turns its task list off on models newer than Opus 4.7 / Sonnet 4.6 / Haiku 4.5.
	// Without it Pitstop sees no plan and cannot forecast. Child process only; the user's own value wins.
	if _, set := os.LookupEnv("CLAUDE_CODE_ENABLE_TODO_TOOLS"); !set {
		c.Env = append(c.Env, "CLAUDE_CODE_ENABLE_TODO_TOOLS=1")
	}
	code := 0
	if err := c.Run(); err != nil {
		code = 127
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
	}
	summary(cfg, id)
	return code
}

// summary closes the run and prints total cost, whether the forecast held, and loop alerts.
func summary(cfg Config, id string) {
	l, err := openLedger(cfg)
	if err != nil {
		return
	}
	defer l.Close()
	outcome, _ := l.EndRun(id)
	spent, _ := l.SpentRun(id)
	line := fmt.Sprintf("pitstop: run %s %s, total %s", id, outcome, usd(spent))
	if acc, _ := l.Accuracy(); true {
		for _, r := range acc.Runs {
			if r.ID == id {
				switch {
				case r.Hit:
					line += fmt.Sprintf(", forecast right (cost to finish %s, range was %s-%s)", usd(r.Rem), usd(r.P10), usd(r.P90))
				case r.Under:
					line += fmt.Sprintf(", forecast WRONG: too high (cost to finish %s, range was %s-%s)", usd(r.Rem), usd(r.P10), usd(r.P90))
				default:
					line += fmt.Sprintf(", forecast WRONG: too low (cost to finish %s, range was %s-%s)", usd(r.Rem), usd(r.P10), usd(r.P90))
				}
			}
		}
	}
	if rs, _ := l.Runs(50); true {
		for _, r := range rs {
			if r.ID == id && r.Loops > 0 {
				line += fmt.Sprintf(", %d loop alerts", r.Loops)
			}
		}
	}
	fmt.Fprintln(os.Stderr, line)
}

func accuracyCmd(cfg Config) int {
	l, err := openLedger(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer l.Close()
	a, _ := l.Accuracy()
	if a.Scored < ledger.MinScored {
		fmt.Printf("accuracy appears after %d finished runs with a forecast (have %d)\n", ledger.MinScored, a.Scored)
	} else {
		fmt.Printf("forecast range right %d of %d finished runs (misses: %d too high, %d too low)\n", a.Hits, a.Scored, a.Under, a.Over)
	}
	for _, r := range a.Runs {
		v := "right"
		if !r.Hit {
			v = "WRONG"
		}
		fmt.Printf("  %-20s forecast %s-%s, actual %s: %s\n", r.ID, usd(r.P10), usd(r.P90), usd(r.Rem), v)
	}
	return 0
}

func runsCmd(cfg Config) int {
	l, err := openLedger(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer l.Close()
	rs, err := l.Runs(20)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("%-20s %-8s %-18s %-16s %5s %5s %9s\n", "RUN", "AGENT", "REPO", "BRANCH", "CALLS", "LOOPS", "COST")
	for _, r := range rs {
		fmt.Printf("%-20s %-8s %-18s %-16s %5d %5d %9s\n", r.ID, r.Agent, r.Repo, r.Branch, r.Calls, r.Loops, money.Exact(r.CostMicro))
	}
	return 0
}
