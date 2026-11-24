package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/proxy"
)

// hookCmd: `pitstop hook pre-tool`. Claude Code runs it before every tool call
// (pitstop run installs it for that session only, through --settings). It asks the
// proxy whether the run may go on; when a decision card is open it waits for the
// answer, so the agent pauses at a safe point instead of inside an HTTP request.
// Exit 0 lets the tool run. Exit 2 blocks it, and Claude reads stderr as the reason.
func hookCmd(cfg Config, args []string) int {
	if len(args) != 1 || args[0] != "pre-tool" {
		fmt.Fprintln(os.Stderr, "usage: pitstop hook pre-tool  (called by Claude Code)")
		return 1 // not 2: a usage mistake must not block the agent's tools
	}
	var in struct {
		SessionID string `json:"session_id"`
	}
	json.NewDecoder(io.LimitReader(os.Stdin, 1<<20)).Decode(&in)
	run := os.Getenv("PITSTOP_RUN")
	if run == "" && in.SessionID != "" {
		run = proxy.SessionRun(in.SessionID)
	}
	if run == "" {
		return 0
	}
	b, _ := json.Marshal(map[string]string{"run": run})
	req, _ := http.NewRequest("POST", "http://"+cfg.Addr+"/pitstop/gate", bytes.NewReader(b))
	req.Header.Set("X-Pitstop", "1")
	req.Header.Set("Content-Type", "application/json")
	// The proxy may hold this while a person decides (default 9 minutes).
	resp, err := (&http.Client{Timeout: 15 * time.Minute}).Do(req)
	if err != nil {
		// Proxy down: every model call fails at the proxy anyway, so don't block tools here.
		fmt.Fprintf(os.Stderr, "pitstop isn't running at %s: start it with `pitstop serve`\n", cfg.Addr)
		return 0
	}
	defer resp.Body.Close()
	var g struct {
		Allow   bool   `json:"allow"`
		Message string `json:"message"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&g) != nil {
		return 0
	}
	if !g.Allow {
		fmt.Fprintln(os.Stderr, g.Message+" Do not start new work: briefly tell the user where the task stands and stop.")
		return 2
	}
	return 0
}

// claudeHookSettings writes the Claude Code settings that install the PreToolUse
// hook and returns the file path for `claude --settings`. Hooks from --settings are
// added to the user's own hooks; the user's settings files are never edited.
func claudeHookSettings() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	cmd := fmt.Sprintf("%q hook pre-tool", filepath.ToSlash(exe)) // forward slashes work in bash, cmd and PowerShell
	s := map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{
		"matcher": "*",
		"hooks":   []any{map[string]any{"type": "command", "command": cmd, "timeout": 600}},
	}}}}
	b, _ := json.MarshalIndent(s, "", "  ")
	path := filepath.Join(filepath.Dir(cfgPath()), "claude-hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, b, 0o600)
}
