package main

import (
	"fmt"
	"os"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/checkpoint"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/ledger"
)

// checkpointCmd: pitstop checkpoint <run>. Saves the repo state to a WIP branch.
func checkpointCmd(cfg Config, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: pitstop checkpoint <run>")
		return 2
	}
	l, err := ledger.Open(cfg.DB)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer l.Close()
	r, err := l.GetRun(args[0])
	if err != nil || r.Dir == "" {
		fmt.Fprintf(os.Stderr, "run %q not found or not inside a git repo\n", args[0])
		return 1
	}
	br, c, err := checkpoint.Make(r.Dir, r.ID, "pitstop checkpoint: manual")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_ = l.SaveCheckpoint(r.ID, br, c, "manual")
	fmt.Printf("saved %s at %.8s\nresume: pitstop resume %s\n", br, c, r.ID)
	return 0
}

// resumeCmd: pitstop resume <run>. Reopens the agent on the same run, so spend and caps continue.
func resumeCmd(cfg Config, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: pitstop resume <run>")
		return 2
	}
	l, err := ledger.Open(cfg.DB)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	r, err := l.GetRun(args[0])
	br, _ := l.LastCheckpoint(args[0])
	if err == nil {
		err = l.SetStopped(r.ID, false) // resuming is the human's yes to continue this run
	}
	l.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "run %q not found or not resumable: %v\n", args[0], err)
		return 1
	}
	if br != "" {
		fmt.Fprintf(os.Stderr, "work is saved on branch %s (git -C %s diff HEAD..%s)\n", br, r.Dir, br)
	}
	var cmd []string
	switch {
	case r.Agent == "claude" && r.SessionID != "":
		cmd = []string{"claude", "--resume", r.SessionID}
	case r.Agent == "claude":
		cmd = []string{"claude", "--continue"} // session id not seen: reopen the latest session in this folder
	case r.Agent == "codex" && r.SessionID != "":
		cmd = []string{"codex", "resume", r.SessionID}
	case r.Agent == "codex":
		cmd = []string{"codex", "resume", "--last"} // most recent session in this folder
	default:
		fmt.Fprintf(os.Stderr, "no automatic resume for %q. Restart it with: pitstop run -- %s  (in %s)\n", r.Agent, r.Agent, r.Dir)
		return 1
	}
	fmt.Fprintf(os.Stderr, "resuming run %s: %v\n", r.ID, cmd)
	return launch(cfg, r.ID, r.Dir, cmd)
}
