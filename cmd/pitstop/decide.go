package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/decide"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/money"
)

func cardText(c decide.Card) string { return c.Text() }

func ctl(cfg Config, method, path string, body any) (*http.Response, error) {
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, "http://"+cfg.Addr+path, r)
	req.Header.Set("X-Pitstop", "1")
	return http.DefaultClient.Do(req)
}

func answer(cfg Config, id string, cont bool, add int64, channel string) error {
	resp, err := ctl(cfg, "POST", "/pitstop/answer", map[string]any{"id": id, "continue": cont, "add_micro": add, "channel": channel})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("answer refused: %s", resp.Status)
	}
	return nil
}

// decideCmd: run in a second terminal. Shows each waiting card and asks Continue or Stop.
func decideCmd(cfg Config) int {
	in := bufio.NewReader(os.Stdin)
	fmt.Fprintln(os.Stderr, "waiting for decision cards (ctrl-c to quit)")
	for {
		resp, err := ctl(cfg, "GET", "/pitstop/pending", nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pitstop proxy not running at %s\n", cfg.Addr)
			return 1
		}
		var cards []decide.Card
		json.NewDecoder(resp.Body).Decode(&cards)
		resp.Body.Close()
		for _, c := range cards {
			fmt.Printf("\n%s", cardText(c))
			fmt.Printf("[Enter] Continue +%s   [number] Continue +$N   [s] Stop & save: ", usd(c.Offer))
			line, _ := in.ReadString('\n')
			line = strings.TrimSpace(strings.ToLower(line))
			cont, add := true, c.Offer
			switch {
			case line == "s" || line == "stop":
				cont, add = false, 0
			case line != "":
				v, err := money.ParseUSD(line)
				if err != nil || v <= 0 {
					fmt.Println("not an amount in dollars: skipping, card stays open")
					continue
				}
				add = v
			}
			if err := answer(cfg, c.ID, cont, add, "cli"); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		}
		time.Sleep(time.Second)
	}
}

// desktopNotify shows a blocking popup with two buttons (macOS osascript, Linux zenity).
// The popup closes itself after the card times out.
func desktopNotify(cfg Config, c decide.Card, timeout time.Duration) {
	cont := fmt.Sprintf("Continue +%s", usd(c.Offer))
	var out []byte
	var err error
	secs := fmt.Sprint(int(timeout.Seconds()))
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`display dialog %q with title "Pitstop" buttons {"Stop & save", %q} default button 2 giving up after %s`,
			cardText(c), cont, secs)
		out, err = exec.Command("osascript", "-e", script).Output()
		if err == nil && strings.Contains(string(out), "button returned:Continue") {
			answer(cfg, c.ID, true, c.Offer, "desktop")
		} else if err == nil && strings.Contains(string(out), "button returned:Stop") {
			answer(cfg, c.ID, false, 0, "desktop")
		}
	case "linux":
		if _, e := exec.LookPath("zenity"); e != nil {
			return
		}
		err = exec.Command("zenity", "--question", "--title=Pitstop", "--text="+cardText(c),
			"--ok-label="+cont, "--cancel-label=Stop & save", "--timeout="+secs).Run()
		if err == nil {
			answer(cfg, c.ID, true, c.Offer, "desktop")
		} else if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			answer(cfg, c.ID, false, 0, "desktop")
		}
	}
}
