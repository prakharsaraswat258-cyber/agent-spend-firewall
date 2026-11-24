package proxy

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/decide"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/ledger"
)

func TestDashboardFiles(t *testing.T) {
	px, _, _ := setupP(t, 0)
	for path, ctype := range map[string]string{
		"/dashboard": "text/html", "/dashboard/app.js": "text/javascript", "/dashboard/app.css": "text/css",
		"/dashboard/fonts/IBMPlexMono-Regular.woff2": "font/woff2", "/dashboard/favicon.svg": "image/svg+xml",
	} {
		resp, err := http.Get(px.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), ctype) {
			t.Errorf("%s: %d %s", path, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") {
			t.Errorf("%s: CSP %q", path, csp)
		}
	}
	for _, bad := range []string{"/dashboard/../proxy.go", "/dashboard/nope.js"} {
		resp, _ := http.Get(px.URL + bad)
		if resp.StatusCode == 200 {
			t.Errorf("%s served", bad)
		}
	}
	req, _ := http.NewRequest("GET", px.URL+"/dashboard", nil)
	req.Host = "evil.example"
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 403 {
		t.Error("dashboard must refuse a foreign Host (DNS rebinding)")
	}
}

func TestControlRejectsForeignOrigin(t *testing.T) {
	px, _, _ := setupP(t, 0)
	req, _ := http.NewRequest("POST", px.URL+"/pitstop/answer", strings.NewReader(`{"id":"x","continue":false}`))
	req.Header.Set("X-Pitstop", "1")
	req.Header.Set("Origin", "https://evil.example")
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 403 {
		t.Fatalf("foreign origin: %d", resp.StatusCode)
	}
}

func TestEventsStreamWithoutCustomHeader(t *testing.T) {
	px, _, _ := setupP(t, 0)
	smallCall(px)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", px.URL+"/pitstop/events", nil) // EventSource can't set headers
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal(resp.Header.Get("Content-Type"))
	}
	line, _ := bufio.NewReader(resp.Body).ReadString('\n')
	if !strings.HasPrefix(line, "data: {") || !strings.Contains(line, `"Runs"`) {
		t.Fatalf("first frame %q", line)
	}
}

func TestRunDetailAndHistory(t *testing.T) {
	px, l, p := setupP(t, 0)
	p.Decider = decide.New(time.Second)
	smallCall(px) // run "dc"
	time.Sleep(50 * time.Millisecond)
	l.SaveCheckpoint("dc", "pitstop/wip/dc", "abc123", "manual")
	get := func(path string) (int, string) {
		req, _ := http.NewRequest("GET", px.URL+path, nil)
		req.Header.Set("X-Pitstop", "1")
		resp, _ := http.DefaultClient.Do(req)
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	c, b := get("/pitstop/run?id=dc")
	for _, want := range []string{`"ID":"dc"`, `"Spent":11475`, `"Calls":1`, `"CallList":[{`, `"Series":[{`, `"Resume":"pitstop resume dc"`, `"Branch":"pitstop/wip/dc"`} {
		if c != 200 || !strings.Contains(b, want) {
			t.Fatalf("run detail missing %s: %d %s", want, c, b)
		}
	}
	if c, _ := get("/pitstop/run?id=nope"); c != 404 {
		t.Fatalf("unknown run: %d", c)
	}
	l.StartRun(ledger.Run{ID: "other", Repo: "r2", Agent: "codex", Started: time.Now()})
	_, b = get("/pitstop/history?agent=codex")
	if !strings.Contains(b, `"ID":"other"`) || strings.Contains(b, `"ID":"dc"`) || !strings.Contains(b, `"Agents":["codex"]`) {
		t.Fatalf("history filter: %s", b)
	}
}

// First answer wins; a second one is refused with 409, and Continue needs an amount.
func TestAnswerConflictAndValidation(t *testing.T) {
	px, _, p := setupP(t, 20_000)
	p.Decider = decide.New(5 * time.Second)
	smallCall(px)
	time.Sleep(50 * time.Millisecond)
	go smallCall(px)
	var c decide.Card
	for i := 0; i < 200 && c.ID == ""; i++ {
		time.Sleep(10 * time.Millisecond)
		if ps := p.Decider.Pending(); len(ps) == 1 {
			c = ps[0]
		}
	}
	post := func(body string) int {
		req, _ := http.NewRequest("POST", px.URL+"/pitstop/answer", strings.NewReader(body))
		req.Header.Set("X-Pitstop", "1")
		resp, _ := http.DefaultClient.Do(req)
		return resp.StatusCode
	}
	if s := post(`{"id":"` + c.ID + `","continue":true,"add_micro":0}`); s != 400 {
		t.Fatalf("continue without an amount: %d", s)
	}
	if s := post(`{"id":"` + c.ID + `","continue":true,"add_micro":1000000,"channel":"dashboard"}`); s != 200 {
		t.Fatalf("first answer: %d", s)
	}
	if s := post(`{"id":"` + c.ID + `","continue":false}`); s != 409 {
		t.Fatalf("second answer must be refused: %d", s)
	}
}
