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
	if err != nil || resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "pitstop proxy not running at %s. Start it: pitstop serve\n", cfg.Addr)
		return 1
	}
	resp.Body.Close()
	l, err := ledger.Open(cfg.DB)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	cwd, _ := os.Getwd()
	repo, branch, top := detect(cwd)
	id := newID()
	if err := l.StartRun(ledger.Run{ID: id, Repo: repo, Branch: branch, Agent: filepath.Base(args[0]), Dir: top, Started: time.Now()}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	l.Close()
	fmt.Fprintf(os.Stderr, "pitstop run %s  repo=%s branch=%s\n", id, repo, branch)
	return launch(cfg, id, "", args)
}

// launch runs cmd with the proxy env for run id, in dir (or here when empty).
func launch(cfg Config, id, dir string, args []string) int {
	base := "http://" + cfg.Addr + "/run/" + id
	c := exec.Command(args[0], args[1:]...)
	c.Dir = dir
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	c.Env = append(os.Environ(), "ANTHROPIC_BASE_URL="+base, "OPENAI_BASE_URL="+base+"/v1")
	if err := c.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 127
	}
	return 0
}

func runsCmd(cfg Config) int {
	l, err := ledger.Open(cfg.DB)
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
		fmt.Printf("%-20s %-8s %-18s %-16s %5d %5d $%8.4f\n", r.ID, r.Agent, r.Repo, r.Branch, r.Calls, r.Loops, float64(r.CostMicro)/1e6)
	}
	return 0
}
