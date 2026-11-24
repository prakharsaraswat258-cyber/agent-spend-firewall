package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/caps"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/ledger"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/price"
)

func TestLoadProviders(t *testing.T) {
	dir := t.TempDir()
	if ps, err := LoadProviders(filepath.Join(dir, "none.json")); ps != nil || err != nil {
		t.Fatal("a missing file means no providers")
	}
	write := func(s string) string {
		p := filepath.Join(dir, "p.json")
		os.WriteFile(p, []byte(s), 0o600)
		return p
	}
	ps, err := LoadProviders(write(`{"providers":{"openrouter":{"base_url":"https://openrouter.ai/api/v1/"},"gw":{"base_url":"http://localhost:8080","kind":"anthropic"}}}`))
	if err != nil || ps["openrouter"].Kind != "openai" || ps["openrouter"].url.Path != "/api/v1" || ps["gw"].Kind != "anthropic" {
		t.Fatalf("%v %+v", err, ps)
	}
	for _, bad := range []string{
		`{"providers":{"bad name":{"base_url":"https://x.com"}}}`,
		`{"providers":{"x":{"base_url":"http://x.com"}}}`, // keys would travel in plain http
		`{"providers":{"x":{"base_url":"https://x.com","kind":"grpc"}}}`,
		`{"providers":{"x":{"base_url":"not a url"}}}`,
		`{nope`,
	} {
		if _, err := LoadProviders(write(bad)); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
}

func TestNamedProviderMeteredAndPriced(t *testing.T) {
	var paths []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		io.WriteString(w, `{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":1000,"completion_tokens":100}}`)
	}))
	defer up.Close()
	u, _ := url.Parse(up.URL + "/api/v1")
	l, _ := ledger.Open(filepath.Join(t.TempDir(), "l.db"))
	t.Cleanup(func() { l.Close() })
	prices := price.Default().WithCustom(map[string]price.Entry{"or/vendor/model-x": {Input: price.R("1"), Output: price.R("2")}})
	p := &Proxy{Prices: prices, Ledger: l, Guard: caps.New(caps.Limits{}, l), DefaultRun: "np",
		Providers: map[string]*Provider{
			"or":  {Kind: "openai", url: u},
			"off": {Kind: "openai", UseOfficialPrices: true, url: u},
		}}
	px := httptest.NewServer(p)
	t.Cleanup(px.Close)
	do := func(method, path, model string) (int, string) {
		req, _ := http.NewRequest(method, px.URL+path, strings.NewReader(`{"model":"`+model+`","max_tokens":100,"messages":[]}`))
		req.Header.Set("Authorization", "Bearer sk-or-test")
		resp, _ := http.DefaultClient.Do(req)
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if c, b := do("POST", "/p/or/chat/completions", "vendor/model-x"); c != 200 {
		t.Fatalf("%d %s", c, b)
	}
	if c, b := do("POST", "/p/or/chat/completions", "gpt-4o"); c != 400 || !strings.Contains(b, "pitstop prices add or/gpt-4o") {
		t.Fatalf("a reseller must not borrow the official price by default: %d %s", c, b)
	}
	if c, _ := do("POST", "/run/r9/p/off/chat/completions", "gpt-4o"); c != 200 {
		t.Fatal("use_official_prices must allow the official price")
	}
	if c, _ := do("GET", "/p/or/models", ""); c != 200 {
		t.Fatal("free read must pass")
	}
	if c, _ := do("POST", "/p/or/embeddings", "x"); c != 400 {
		t.Fatal("unpriceable POST must be blocked")
	}
	if c, b := do("POST", "/p/nobody/chat/completions", "x"); c != 404 || !strings.Contains(b, "providers.json") {
		t.Fatalf("unknown provider: %d %s", c, b)
	}
	if len(paths) != 3 || paths[0] != "POST /api/v1/chat/completions Bearer sk-or-test" || !strings.HasPrefix(paths[2], "GET /api/v1/models") {
		t.Fatalf("upstream saw %v", paths)
	}
	var cs []ledger.CallRow
	for i := 0; i < 100 && len(cs) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
		cs, _ = l.Calls("np", 10)
	}
	// 1000 x $1 + 100 x $2 per MTok = 1200 micro-dollars
	if len(cs) != 1 || cs[0].CostMicro != 1200 {
		t.Fatalf("%+v", cs)
	}
	if s, _ := l.SpentRun("r9"); s == 0 {
		t.Fatal("run id in the path must work with named providers")
	}
}

func TestGateRecordsSessionForResume(t *testing.T) {
	px, l, _ := setupP(t, 0)
	post := func(body string) {
		req, _ := http.NewRequest("POST", px.URL+"/pitstop/gate", strings.NewReader(body))
		req.Header.Set("X-Pitstop", "1")
		resp, _ := http.DefaultClient.Do(req)
		io.Copy(io.Discard, resp.Body)
	}
	l.StartRun(ledger.Run{ID: "cx", Agent: "codex", Started: time.Now()})
	post(`{"run":"cx","session":"not-a-uuid; rm -rf /"}`)
	if r, _ := l.GetRun("cx"); r.SessionID != "" {
		t.Fatal("non-uuid session stored")
	}
	post(`{"run":"cx","session":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"}`)
	if r, _ := l.GetRun("cx"); r.SessionID != "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b" {
		t.Fatalf("session %q", r.SessionID)
	}
}
