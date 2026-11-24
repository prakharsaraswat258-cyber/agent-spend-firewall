package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/caps"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/decide"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/ledger"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/price"
)

const sse = `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":1000,"cache_read_input_tokens":2000,"cache_creation_input_tokens":100,"output_tokens":1}}}

event: message_delta
data: {"type":"message_delta","usage":{"output_tokens":500}}

`

func setup(t *testing.T, runCap int64) (*httptest.Server, *ledger.Ledger) {
	px, l, _ := setupP(t, runCap)
	return px, l
}

func setupP(t *testing.T, runCap int64) (*httptest.Server, *ledger.Ledger, *Proxy) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sse)
	}))
	t.Cleanup(up.Close)
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(up.URL)
	p := &Proxy{Prices: price.Default(), Ledger: l, Guard: caps.New(caps.Limits{Run: runCap}, l),
		Anthropic: u, OpenAI: u, DefaultRun: "r1"}
	px := httptest.NewServer(p)
	t.Cleanup(px.Close)
	return px, l, p
}

func post(px *httptest.Server) (*http.Response, string) {
	resp, _ := http.Post(px.URL+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"claude-sonnet-4","max_tokens":1000,"stream":true,"messages":[]}`))
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestStreamMeteredExactly(t *testing.T) {
	px, l := setup(t, 0)
	resp, body := post(px)
	if resp.StatusCode != 200 || body != sse {
		t.Fatalf("pass-through changed: %d %q", resp.StatusCode, body)
	}
	var got int64
	for i := 0; i < 100 && got == 0; i++ { // ledger write lands just after the client sees EOF
		time.Sleep(10 * time.Millisecond)
		got, _ = l.SpentRun("r1")
	}
	// 1000*3 + 500*15 + 2000*0.3 + 100*3.75 = 11475
	if got != 11475 {
		t.Fatalf("cost %d want 11475", got)
	}
}

func TestCapBlocksBeforeSending(t *testing.T) {
	px, l := setup(t, 500_000) // $0.50; worst case for 1000 max tokens is ~$0.015
	for i := 0; i < 100; i++ {
		resp, _ := post(px)
		if resp.StatusCode == 429 {
			spent, _ := l.SpentRun("r1")
			if spent > 500_000 {
				t.Fatalf("overshot cap: %d", spent)
			}
			return
		}
	}
	t.Fatal("cap never hit")
}

func TestUnknownModelBlocked(t *testing.T) {
	px, _ := setup(t, 0)
	resp, _ := http.Post(px.URL+"/v1/messages", "application/json", strings.NewReader(`{"model":"zzz"}`))
	if resp.StatusCode != 400 {
		t.Fatal(resp.StatusCode)
	}
}

func TestRunIDInPath(t *testing.T) {
	px, l := setup(t, 0)
	resp, _ := http.Post(px.URL+"/run/abc/v1/messages", "application/json",
		strings.NewReader(`{"model":"claude-sonnet-4","max_tokens":10,"stream":true}`))
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	var got int64
	for i := 0; i < 100 && got == 0; i++ {
		time.Sleep(10 * time.Millisecond)
		got, _ = l.SpentRun("abc")
	}
	if got != 11475 {
		t.Fatalf("run abc cost %d", got)
	}
}

func TestLoopStoppedAtFive(t *testing.T) {
	px, l := setup(t, 0)
	send := func(n int) int {
		var ms []string
		for i := 0; i < n; i++ {
			ms = append(ms, `{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"c":"ls"}}]}`,
				`{"role":"user","content":[{"type":"tool_result","content":"x"}]}`)
		}
		resp, _ := http.Post(px.URL+"/run/lp/v1/messages", "application/json",
			strings.NewReader(`{"model":"claude-sonnet-4","max_tokens":10,"messages":[`+strings.Join(ms, ",")+`]}`))
		return resp.StatusCode
	}
	if c := send(4); c != 200 {
		t.Fatalf("4 repeats must warn only, got %d", c)
	}
	if c := send(5); c != 429 {
		t.Fatalf("5 repeats must stop, got %d", c)
	}
	rs, _ := l.Runs(10)
	for _, r := range rs {
		if r.ID == "lp" && r.Loops == 2 {
			return
		}
	}
	t.Fatalf("want 2 alerts on run lp: %+v", rs)
}

func TestLoopStopSavesWork(t *testing.T) {
	px, l := setup(t, 0)
	d := t.TempDir()
	sh := func(a ...string) {
		c := exec.Command("git", append([]string{"-C", d, "-c", "user.name=t", "-c", "user.email=t@t"}, a...)...)
		if o, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, o)
		}
	}
	sh("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(d, "f"), []byte("1"), 0o644)
	sh("add", "-A")
	sh("commit", "-qm", "i")
	os.WriteFile(filepath.Join(d, "f"), []byte("work in progress"), 0o644)
	l.StartRun(ledger.Run{ID: "sv", Dir: d, Started: time.Now()})
	var ms []string
	for i := 0; i < 5; i++ {
		ms = append(ms, `{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{}}]}`,
			`{"role":"user","content":[{"type":"tool_result","content":"x"}]}`)
	}
	resp, _ := http.Post(px.URL+"/run/sv/v1/messages", "application/json",
		strings.NewReader(`{"model":"claude-sonnet-4","messages":[`+strings.Join(ms, ",")+`]}`))
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 429 || !strings.Contains(string(b), "pitstop resume sv") {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	out, _ := exec.Command("git", "-C", d, "show", "pitstop/wip/sv:f").Output()
	if string(out) != "work in progress" {
		t.Fatalf("wip branch content %q", out)
	}
}

func TestProgressTracked(t *testing.T) {
	px, l := setup(t, 0)
	todos := `{"todos":[{"status":"completed"},{"status":"completed"},{"status":"completed"},{"status":"pending"},{"status":"pending"}]}`
	http.Post(px.URL+"/run/pg/v1/messages", "application/json", strings.NewReader(
		`{"model":"claude-sonnet-4","max_tokens":10,"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"TodoWrite","input":`+todos+`}]}]}`))
	r, err := l.GetRun("pg")
	if err != nil || r.StepsDone != 3 || r.StepsTotal != 5 {
		t.Fatalf("%+v %v", r, err)
	}
}

func smallCall(px *httptest.Server) int {
	resp, _ := http.Post(px.URL+"/run/dc/v1/messages", "application/json",
		strings.NewReader(`{"model":"claude-sonnet-4","max_tokens":1000,"stream":true,"messages":[]}`))
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func TestCardOnCapContinueRaisesBudget(t *testing.T) {
	px, _, p := setupP(t, 20_000)
	p.Decider = decide.New(5 * time.Second)
	if smallCall(px) != 200 {
		t.Fatal("first call fits")
	}
	time.Sleep(50 * time.Millisecond) // ledger write lands after EOF
	done := make(chan int)
	go func() { done <- smallCall(px) }() // would exceed cap: must pause, not fail
	var c decide.Card
	for i := 0; i < 200 && c.ID == ""; i++ {
		time.Sleep(10 * time.Millisecond)
		if ps := p.Decider.Pending(); len(ps) == 1 {
			c = ps[0]
		}
	}
	if c.ID == "" || c.Reason != "cap" {
		t.Fatalf("no cap card: %+v", c)
	}
	select {
	case <-done:
		t.Fatal("call must wait for the human")
	case <-time.After(50 * time.Millisecond):
	}
	if err := p.Respond(c.ID, true, 1_000_000, "test"); err != nil {
		t.Fatal(err)
	}
	if code := <-done; code != 200 {
		t.Fatalf("after Continue: %d", code)
	}
	if p.Guard.RunCap("dc") != 1_020_000 {
		t.Fatalf("cap %d", p.Guard.RunCap("dc"))
	}
}

func TestCardTimeoutStopsAndSaves(t *testing.T) {
	px, _, p := setupP(t, 20_000)
	p.Decider = decide.New(80 * time.Millisecond)
	smallCall(px)
	time.Sleep(50 * time.Millisecond)
	if c := smallCall(px); c != 429 {
		t.Fatalf("no answer must stop, got %d", c)
	}
}

func TestControlRejectsForeignRequests(t *testing.T) {
	px, _, p := setupP(t, 0)
	p.Decider = decide.New(time.Second)
	get := func(host, hdr string) int {
		req, _ := http.NewRequest("GET", px.URL+"/pitstop/pending", nil)
		if host != "" {
			req.Host = host
		}
		if hdr != "" {
			req.Header.Set("X-Pitstop", hdr)
		}
		resp, _ := http.DefaultClient.Do(req)
		return resp.StatusCode
	}
	if get("", "") != 403 || get("evil.com", "1") != 403 || get("", "1") != 200 {
		t.Fatal("control endpoint guards wrong")
	}
}
