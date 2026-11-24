package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/caps"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/ledger"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/price"
)

const sse = `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":1000,"cache_read_input_tokens":2000,"cache_creation_input_tokens":100,"output_tokens":1}}}

event: message_delta
data: {"type":"message_delta","usage":{"output_tokens":500}}

`

func setup(t *testing.T, runCap int64) (*httptest.Server, *ledger.Ledger) {
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
	return px, l
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
