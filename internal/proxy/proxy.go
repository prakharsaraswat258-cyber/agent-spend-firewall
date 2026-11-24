// Package proxy is a streaming pass-through for Anthropic and OpenAI-style APIs.
// It reserves worst-case cost before each call, meters actual usage after, and
// never stores prompts or API keys.
package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/caps"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/checkpoint"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/decide"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/forecast"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/ledger"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/loop"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/price"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/progress"
)

const defaultMaxOutput = 32000 // used when the request sets no max tokens

type Proxy struct {
	Prices     *price.Table
	Ledger     *ledger.Ledger
	Guard      *caps.Guard
	Anthropic  *url.URL
	OpenAI     *url.URL
	DefaultRun string
	Decider    *decide.Decider // nil: no pause card, caps block with 429
}

var sessionRe = regexp.MustCompile(`session_([0-9a-fA-F-]{36})`)

// save checkpoints the run's repo (best effort) and returns a line for the error message.
func (p *Proxy) save(run, reason string) string {
	r, err := p.Ledger.GetRun(run)
	if err != nil || r.Dir == "" {
		return ""
	}
	br, c, err := checkpoint.Make(r.Dir, run, "pitstop checkpoint: "+reason)
	if err != nil {
		log.Printf("pitstop: checkpoint failed: %v", err)
		return ""
	}
	_ = p.Ledger.SaveCheckpoint(run, br, c, reason)
	return fmt.Sprintf(" Work saved on branch %s. Resume: pitstop resume %s", br, run)
}

// admit reserves the call's worst-case cost, pausing on a decision card first when
// the forecast says the run will not fit, or the run cap is hit.
func (p *Proxy) admit(run string, need int64) error {
	if p.Decider == nil {
		return p.Guard.Reserve(run, need)
	}
	for i := 0; i < 3; i++ {
		if c, ok := p.forecastCard(run); ok {
			if err := p.ask(run, c); err != nil {
				return err
			}
			continue // budget raised: re-check
		}
		err := p.Guard.Reserve(run, need)
		var d *caps.Denied
		if errors.As(err, &d) && d.Scope == "run" {
			c := p.baseCard(run, "cap")
			c.Spent, c.Budget = d.Used, d.Cap
			c.Offer = max(d.Cap/2, d.Need) // no plan: offer half the budget again
			if err := p.ask(run, c); err != nil {
				return err
			}
			continue
		}
		return err // nil (reserved) or a day/month cap: owner-only, no card
	}
	return errors.New("run budget still exhausted after 3 approvals")
}

func (p *Proxy) baseCard(run, reason string) decide.Card {
	spent, _ := p.Ledger.SpentRun(run)
	return decide.Card{Run: run, Reason: reason, Spent: spent, Budget: p.Guard.RunCap(run)}
}

func (p *Proxy) forecastCard(run string) (decide.Card, bool) {
	c := p.baseCard(run, "forecast")
	r, err := p.Ledger.GetRun(run)
	if err != nil || r.StepsTotal == 0 {
		return c, false
	}
	costs, _ := p.Ledger.CallCosts(run)
	f := forecast.Compute(c.Spent, costs, r.StepsDone, r.StepsTotal)
	if !forecast.WillOverrun(c.Spent, c.Budget, f) {
		return c, false
	}
	c.P50, c.P90, c.Confidence, c.Done, c.Total = f.P50, f.P90, f.Confidence, r.StepsDone, r.StepsTotal
	c.Offer = max(c.Spent+f.P90-c.Budget, 1) // raise budget enough to cover the p90 finish
	return c, true
}

// ask pauses the call. nil means Continue (budget already raised); an error means stop.
func (p *Proxy) ask(run string, c decide.Card) error {
	a := p.Decider.Ask(c)
	if a.Continue {
		return nil
	}
	if a.TimedOut {
		return errors.New("no answer to the pause card in time: stopped (never auto-continues).")
	}
	return errors.New("you chose Stop.")
}

// Respond answers a pending card. Continue raises the run budget by add.
func (p *Proxy) Respond(id string, cont bool, add int64, channel string) error {
	c, ok := p.Decider.Card(id)
	if !ok {
		return errors.New("no such pending card")
	}
	if cont {
		p.Guard.Raise(c.Run, add)
	}
	p.Decider.Resolve(id, decide.Answer{Continue: cont, Add: add, Channel: channel})
	return nil
}

// trackProgress stores the agent's plan state and saves a forecast whenever a step completes.
func (p *Proxy) trackProgress(run string, body []byte) {
	pr := progress.Parse(body)
	if !pr.Found {
		return
	}
	changed, err := p.Ledger.SetProgress(run, pr.Done, pr.Total)
	if err != nil || !changed {
		return
	}
	spent, _ := p.Ledger.SpentRun(run)
	costs, _ := p.Ledger.CallCosts(run)
	if f := forecast.Compute(spent, costs, pr.Done, pr.Total); f.HasPlan {
		_ = p.Ledger.SaveForecast(run, spent, f.P50, f.P90, f.Confidence, pr.Done, pr.Total)
	}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// /run/<id>/... carries the run id in the base URL, so any tool with a base-URL setting works.
	if strings.HasPrefix(r.URL.Path, "/pitstop/") {
		p.Control(w, r)
		return
	}
	runID := ""
	if rest, ok := strings.CutPrefix(r.URL.Path, "/run/"); ok {
		id, path, _ := strings.Cut(rest, "/")
		runID, r.URL.Path = id, "/"+path
	}
	var provider string
	var target *url.URL
	switch {
	case strings.HasPrefix(r.URL.Path, "/v1/messages"):
		provider, target = "anthropic", p.Anthropic
	case strings.HasPrefix(r.URL.Path, "/v1/chat/completions"), strings.HasPrefix(r.URL.Path, "/v1/responses"):
		provider, target = "openai", p.OpenAI
	default:
		// Non-metered paths (model lists etc.) go to the OpenAI-style upstream untouched.
		p.forward(w, r, p.OpenAI, nil)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		fail(w, provider, 400, "cannot read request body")
		return
	}
	var req struct {
		Model     string `json:"model"`
		Stream    bool   `json:"stream"`
		MaxTokens int64  `json:"max_tokens"`
		MaxComp   int64  `json:"max_completion_tokens"`
		MaxOut    int64  `json:"max_output_tokens"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Model == "" {
		fail(w, provider, 400, "request has no JSON model field")
		return
	}
	entry, err := p.Prices.Lookup(req.Model)
	if err != nil {
		fail(w, provider, 400, err.Error()) // unknown price: block with a fix
		return
	}
	maxOut := firstPositive(req.MaxTokens, req.MaxComp, req.MaxOut, defaultMaxOutput)
	// bytes/3 over-counts tokens: safe upper bound for reservation.
	need := entry.MaxCost(int64(len(body))/3+1, maxOut)

	run := runID
	if run == "" {
		run = r.Header.Get("X-Pitstop-Run")
	}
	if run == "" {
		run = p.DefaultRun
	}
	if provider == "anthropic" && runID != "" {
		if m := sessionRe.FindSubmatch(body); m != nil {
			_ = p.Ledger.SetSession(run, string(m[1])) // lets resume reopen the same Claude Code session
		}
	}
	if provider == "anthropic" {
		p.trackProgress(run, body)
	}
	if lr := loop.Detect(body); lr.Count >= loop.Warn {
		action := "warned"
		if lr.Count >= loop.Stop {
			action = "stopped"
		}
		if err := p.Ledger.RecordLoop(run, lr.Kind, lr.Tool, lr.Count, action); err != nil {
			log.Printf("pitstop: loop alert write failed: %v", err)
		}
		if action == "stopped" {
			fail(w, provider, 429, fmt.Sprintf("Pitstop stopped a loop: %s %q repeated %d times.", lr.Kind, lr.Tool, lr.Count)+p.save(run, "loop"))
			return
		}
	}
	if err := p.admit(run, need); err != nil {
		fail(w, provider, 429, "Pitstop blocked this call: "+err.Error()+p.save(run, "stop"))
		return
	}
	defer p.Guard.Release(run, need)

	if provider == "openai" && req.Stream && strings.HasPrefix(r.URL.Path, "/v1/chat/completions") {
		body = withUsageStreaming(body)
	}
	start := time.Now()
	m := &meter{provider: provider, stream: req.Stream}
	p.forward(w, r, target, &tap{body: body, m: m})
	u := m.usage()
	if u == (price.Usage{}) {
		return // upstream error or no usage reported: nothing to bill
	}
	if err := p.Ledger.Record(ledger.Call{
		RunID: run, Provider: provider, Model: req.Model,
		Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite,
		CostMicro: entry.Cost(u), LatencyMS: time.Since(start).Milliseconds(), At: start,
	}); err != nil {
		log.Printf("pitstop: ledger write failed: %v", err)
	}
}

type tap struct {
	body []byte
	m    *meter
}

func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, target *url.URL, t *tap) {
	rp := &httputil.ReverseProxy{
		FlushInterval: -1, // flush every write: SSE must stream
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
			pr.Out.Header.Del("X-Pitstop-Run")
			pr.Out.Header.Del("Accept-Encoding") // so the transport gunzips and the meter can read usage
			if t != nil {
				pr.Out.Body = io.NopCloser(bytes.NewReader(t.body))
				pr.Out.ContentLength = int64(len(t.body))
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			if t != nil && resp.StatusCode == 200 {
				resp.Body = &teeBody{ReadCloser: resp.Body, m: t.m}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "Pitstop: upstream unreachable: "+err.Error(), http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r)
}

func fail(w http.ResponseWriter, provider string, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if provider == "anthropic" {
		fmt.Fprintf(w, `{"type":"error","error":{"type":"invalid_request_error","message":%q}}`, msg)
		return
	}
	fmt.Fprintf(w, `{"error":{"message":%q,"type":"pitstop_blocked"}}`, msg)
}

func firstPositive(v ...int64) int64 {
	for _, n := range v {
		if n > 0 {
			return n
		}
	}
	return 0
}

// withUsageStreaming asks OpenAI to send a final usage chunk.
func withUsageStreaming(body []byte) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return body
	}
	m["stream_options"] = json.RawMessage(`{"include_usage":true}`)
	if out, err := json.Marshal(m); err == nil {
		return out
	}
	return body
}

// teeBody copies bytes to the meter as the client reads them.
type teeBody struct {
	io.ReadCloser
	m *meter
}

func (t *teeBody) Read(p []byte) (int, error) {
	n, err := t.ReadCloser.Read(p)
	if n > 0 {
		t.m.write(p[:n])
	}
	return n, err
}
