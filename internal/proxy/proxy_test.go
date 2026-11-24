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
	t.Cleanup(func() { l.Close() }) // Windows cannot delete an open file
	u, _ := url.Parse(up.URL)
	p := &Proxy{Prices: price.Default(), Ledger: l, Guard: caps.New(caps.Limits{Run: runCap}, l),
		Anthropic: u, OpenAI: u, DefaultRun: "r1"}
	px := httptest.NewServer(p)
	t.Cleanup(px.Close)
	return px, l, p
}

func post(px *httptest.Server) (*http.Response, string) {
	resp, _ := http.Post(px.URL+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"claude-sonnet-4-5","max_tokens":1000,"stream":true,"messages":[]}`))
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
		if resp.StatusCode == 403 {
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
		strings.NewReader(`{"model":"claude-sonnet-4-5","max_tokens":10,"stream":true}`))
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
			strings.NewReader(`{"model":"claude-sonnet-4-5","max_tokens":10,"messages":[`+strings.Join(ms, ",")+`]}`))
		return resp.StatusCode
	}
	if c := send(4); c != 200 {
		t.Fatalf("4 repeats must warn only, got %d", c)
	}
	if c := send(5); c != 403 {
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
		strings.NewReader(`{"model":"claude-sonnet-4-5","messages":[`+strings.Join(ms, ",")+`]}`))
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 403 || !strings.Contains(string(b), "pitstop resume sv") {
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
		`{"model":"claude-sonnet-4-5","max_tokens":10,"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"TodoWrite","input":`+todos+`}]}]}`))
	r, err := l.GetRun("pg")
	if err != nil || r.StepsDone != 3 || r.StepsTotal != 5 {
		t.Fatalf("%+v %v", r, err)
	}
}

func smallCall(px *httptest.Server) int {
	resp, _ := http.Post(px.URL+"/run/dc/v1/messages", "application/json",
		strings.NewReader(`{"model":"claude-sonnet-4-5","max_tokens":1000,"stream":true,"messages":[]}`))
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
	if c := smallCall(px); c != 403 {
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

func TestDashboardAndOverview(t *testing.T) {
	px, _, p := setupP(t, 0)
	p.Decider = decide.New(time.Second)
	resp, _ := http.Get(px.URL + "/dashboard")
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), "Pitstop") || resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("dashboard page")
	}
	smallCall(px)
	time.Sleep(50 * time.Millisecond)
	req, _ := http.NewRequest("GET", px.URL+"/pitstop/overview", nil)
	req.Header.Set("X-Pitstop", "1")
	r2, _ := http.DefaultClient.Do(req)
	b, _ = io.ReadAll(r2.Body)
	if !strings.Contains(string(b), `"ID":"dc"`) || !strings.Contains(string(b), `"Spent":11475`) {
		t.Fatalf("overview: %s", b)
	}
}

func TestUnmeteredPathsGoToTheKeysProvider(t *testing.T) {
	hit := func(name string, got *string) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { *got = name }))
		t.Cleanup(s.Close)
		return s
	}
	var got string
	a, o := hit("anthropic", &got), hit("openai", &got)
	au, _ := url.Parse(a.URL)
	ou, _ := url.Parse(o.URL)
	l, _ := ledger.Open(filepath.Join(t.TempDir(), "l.db"))
	t.Cleanup(func() { l.Close() })
	px := httptest.NewServer(&Proxy{Prices: price.Default(), Ledger: l, Guard: caps.New(caps.Limits{}, l), Anthropic: au, OpenAI: ou})
	t.Cleanup(px.Close)
	get := func(h, v string) (string, int) {
		got = ""
		req, _ := http.NewRequest("GET", px.URL+"/v1/models", nil)
		if h != "" {
			req.Header.Set(h, v)
		}
		resp, _ := http.DefaultClient.Do(req)
		return got, resp.StatusCode
	}
	if g, _ := get("x-api-key", "sk-ant-test"); g != "anthropic" {
		t.Fatalf("anthropic key went to %q", g)
	}
	if g, _ := get("Authorization", "Bearer sk-test"); g != "openai" {
		t.Fatalf("openai key went to %q", g)
	}
	if g, c := get("", ""); g != "" || c != 404 {
		t.Fatalf("no credentials must not be forwarded: %q %d", g, c)
	}
}

func TestSessionIDOnlyFromMetadata(t *testing.T) {
	px, l := setup(t, 0)
	l.StartRun(ledger.Run{ID: "ss", Started: time.Now()})
	real, quoted := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	body := `{"model":"claude-sonnet-4-5","max_tokens":10,"metadata":{"user_id":"user_x_session_` + real + `"},` +
		`"messages":[{"role":"user","content":"log says session_` + quoted + `"}]}`
	resp, _ := http.Post(px.URL+"/run/ss/v1/messages", "application/json", strings.NewReader(body))
	io.Copy(io.Discard, resp.Body)
	if r, _ := l.GetRun("ss"); r.SessionID != real {
		t.Fatalf("session %q, want %q", r.SessionID, real)
	}
}

// Ctrl-C mid-stream: the provider still bills, so the ledger must too.
func TestClientHangUpStillBilled(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100000,\"output_tokens\":1}}}\n\n")
		io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello world!\"}}\n\n")
		w.(http.Flusher).Flush()
		select { // never sends message_delta
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer close(release)
	t.Cleanup(up.Close)
	l, _ := ledger.Open(filepath.Join(t.TempDir(), "l.db"))
	t.Cleanup(func() { l.Close() })
	u, _ := url.Parse(up.URL)
	px := httptest.NewServer(&Proxy{Prices: price.Default(), Ledger: l, Guard: caps.New(caps.Limits{}, l), Anthropic: u, OpenAI: u, DefaultRun: "r"})
	t.Cleanup(px.Close)
	resp, err := http.Post(px.URL+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"claude-sonnet-4-5","max_tokens":8000,"stream":true,"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	resp.Body.Read(buf)
	resp.Body.Close() // user hits Ctrl-C
	var cs []ledger.CallRow
	for i := 0; i < 300 && len(cs) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
		cs, _ = l.Calls("r", 10)
	}
	if len(cs) != 1 || cs[0].Input != 100000 || cs[0].Output < 2 || !cs[0].Estimated {
		t.Fatalf("hang-up not billed: %+v", cs)
	}
}

// A failed ledger write must stop later calls instead of letting them through unmetered.
func TestLedgerFailureBlocksLaterCalls(t *testing.T) {
	px, l, _ := setupP(t, 0)
	if c, _ := post(px); c.StatusCode != 200 {
		t.Fatal("first call")
	}
	time.Sleep(50 * time.Millisecond)
	l.Close() // every write now fails
	post(px)  // goes out (already admitted), its write fails
	time.Sleep(50 * time.Millisecond)
	resp, body := post(px)
	if resp.StatusCode != 503 || !strings.Contains(body, "ledger could not be written") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

func TestSideEndpoints(t *testing.T) {
	var hits []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path)
		io.WriteString(w, `{"input_tokens":12}`)
	}))
	t.Cleanup(up.Close)
	l, _ := ledger.Open(filepath.Join(t.TempDir(), "l.db"))
	t.Cleanup(func() { l.Close() })
	u, _ := url.Parse(up.URL)
	px := httptest.NewServer(&Proxy{Prices: price.Default(), Ledger: l, Guard: caps.New(caps.Limits{Run: 1}, l), Anthropic: u, OpenAI: u, DefaultRun: "r"})
	t.Cleanup(px.Close)
	do := func(method, path, auth string) int {
		req, _ := http.NewRequest(method, px.URL+path, strings.NewReader(`{"model":"claude-sonnet-4-5","messages":[]}`))
		if strings.HasPrefix(auth, "Bearer") {
			req.Header.Set("Authorization", auth)
		} else {
			req.Header.Set("x-api-key", auth)
		}
		resp, _ := http.DefaultClient.Do(req)
		io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}
	// Free token counting passes even with a $0.000001 cap, and is not billed.
	if c := do("POST", "/v1/messages/count_tokens", "k"); c != 200 {
		t.Fatalf("count_tokens %d", c)
	}
	if c := do("GET", "/v1/messages/batches/b1", "k"); c != 200 {
		t.Fatalf("batch read %d", c)
	}
	for _, p := range []string{"/v1/messages/batches", "/v1/complete"} {
		if c := do("POST", p, "k"); c != 400 {
			t.Fatalf("%s must be blocked, got %d", p, c)
		}
	}
	for _, p := range []string{"/v1/embeddings", "/v1/images/generations", "/v1/completions"} {
		if c := do("POST", p, "Bearer sk"); c != 400 {
			t.Fatalf("%s must be blocked, got %d", p, c)
		}
	}
	if len(hits) != 2 {
		t.Fatalf("only the 2 free calls may reach the provider: %v", hits)
	}
	if s, _ := l.SpentRun("r"); s != 0 {
		t.Fatalf("free calls billed %d", s)
	}
}
