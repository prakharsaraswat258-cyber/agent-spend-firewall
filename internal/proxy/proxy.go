// Package proxy is a streaming pass-through for Anthropic and OpenAI-style APIs.
// It reserves worst-case cost before each call, meters actual usage after, and
// never stores prompts or API keys.
package proxy

import (
	"bytes"
	"cmp"
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
	"sync"
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
	DefaultRun string               // run for calls with no run id; "" = per Claude Code session, else untagged
	Providers  map[string]*Provider // named upstreams at /p/<name>/...
	Decider    *decide.Decider      // nil: no pause card, caps block with 403

	mu     sync.Mutex
	broken error // set when a ledger write failed: every metered call is blocked until restart
	bandAt time.Time
	bandV  forecast.Band
	typV   time.Duration
	// Per run, from the latest request: the step in progress and the last tool names.
	current map[string]string
	actions map[string][]string
}

// band is the calibrated forecast range, recomputed at most once a minute.
func (p *Proxy) band() forecast.Band {
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Since(p.bandAt) > time.Minute {
		p.bandV, p.typV, p.bandAt = p.Ledger.Band(), p.Ledger.TypicalRunLength(), time.Now()
	}
	return p.bandV
}

func (p *Proxy) typical() time.Duration {
	p.band()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.typV
}

// Forecast is the run's cost-to-finish: from its plan when one was seen, else
// from its spending rate (always low confidence). Not Valid when neither works yet.
func (p *Proxy) Forecast(run string, spent int64) (forecast.Forecast, ledger.Run) {
	r, _ := p.Ledger.GetRun(run)
	if r.StepsTotal > 0 {
		costs, _ := p.Ledger.CallCosts(run)
		return forecast.ComputeWith(p.band(), spent, costs, r.StepsDone, r.StepsTotal), r
	}
	span, _ := p.Ledger.ActiveSpan(run)
	return forecast.Rate(p.band(), spent, span, p.typical()), r
}

func (p *Proxy) setRunState(run, step string, actions []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current == nil {
		p.current, p.actions = map[string]string{}, map[string][]string{}
	}
	p.current[run] = step
	if len(actions) > 0 {
		p.actions[run] = actions
	}
}

func (p *Proxy) runState(run string) (step string, actions []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.current[run], p.actions[run]
}

var sessionRe = regexp.MustCompile(`session_([0-9a-fA-F-]{36})`)

// SessionRun names the run for a Claude Code session that was not started with
// `pitstop run`, so each session gets its own run and run budget.
func SessionRun(sessionID string) string {
	if len(sessionID) > 8 {
		sessionID = sessionID[:8]
	}
	return "cc-" + sessionID
}

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
			c.Offer = offer(max(d.Cap/2, d.Need)) // offer half the budget again
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
	_, actions := p.runState(run)
	return decide.Card{Run: run, Reason: reason, Spent: spent, Budget: p.Guard.RunCap(run), LastActions: actions}
}

// offer rounds a budget raise up to the next $0.50, so the card shows a plain number.
func offer(micro int64) int64 {
	const step = 500_000
	return max((micro+step-1)/step*step, step)
}

func (p *Proxy) forecastCard(run string) (decide.Card, bool) {
	c := p.baseCard(run, "forecast")
	f, r := p.Forecast(run, c.Spent)
	if !forecast.WillOverrun(c.Spent, c.Budget, f) {
		return c, false
	}
	c.P10, c.P50, c.P90, c.Confidence, c.Method = f.P10, f.P50, f.P90, f.Confidence, f.Method
	if f.HasPlan {
		c.Done, c.Total = r.StepsDone, r.StepsTotal
	}
	c.Offer = offer(c.Spent + f.P90 - c.Budget) // enough to cover the high end, rounded up to $0.50
	return c, true
}

// StopError means a human chose Stop, or nobody answered in time. The run stays
// stopped until `pitstop resume`, so a late "yes" cannot restart spending.
type StopError struct{ msg string }

func (e *StopError) Error() string { return e.msg }

// ask pauses the call. nil means Continue (budget already raised); a *StopError means stop.
func (p *Proxy) ask(run string, c decide.Card) error {
	a := p.Decider.Ask(c)
	if a.Continue {
		return nil
	}
	if a.TimedOut {
		return &StopError{"no answer to the pause card in time: stopped (never auto-continues)."}
	}
	return &StopError{"you chose Stop."}
}

// refuse blocks a run's calls: a stopped run stays stopped, and admission errors
// (caps, Stop answers) come back as one message with the checkpoint line.
// It returns "" when the call may go ahead; need has then been reserved.
func (p *Proxy) refuse(run string, need int64) string {
	if stopped, err := p.Ledger.Stopped(run); stopped {
		if err != nil {
			return "Pitstop blocked this call: cannot read the run's state (" + err.Error() + ")."
		}
		return "Pitstop: this run was stopped. Work is saved; continue with: pitstop resume " + run
	}
	err := p.admit(run, need)
	if err == nil {
		return ""
	}
	var se *StopError
	if errors.As(err, &se) {
		if e := p.Ledger.SetStopped(run, true); e != nil {
			log.Printf("pitstop: could not mark run %s stopped: %v", run, e)
		}
	}
	return "Pitstop blocked this call: " + err.Error() + p.save(run, "stop")
}

// Gate is the safe-point check the PreToolUse hook calls before each tool use.
// It pauses on a decision card when the next call would not fit, or the forecast
// says the run won't finish in budget; "" means go ahead.
func (p *Proxy) Gate(run string) string {
	need := p.nextCallEstimate(run)
	msg := p.refuse(run, need)
	if msg == "" {
		p.Guard.Release(run, need) // a check only: the model call reserves for itself
	}
	return msg
}

// nextCallEstimate: the next call likely costs about what the last few did.
func (p *Proxy) nextCallEstimate(run string) int64 {
	costs, _ := p.Ledger.CallCosts(run)
	if len(costs) == 0 {
		return 1
	}
	tail := costs[max(0, len(costs)-3):]
	var sum int64
	for _, c := range tail {
		sum += c
	}
	return max(sum/int64(len(tail)), 1)
}

// Respond answers a pending card. Continue raises the run budget by add.
func (p *Proxy) Respond(id string, cont bool, add int64, channel string) error {
	c, ok := p.Decider.Card(id)
	if !ok {
		return errors.New("no such pending card")
	}
	if cont {
		if err := p.Guard.Raise(c.Run, add); err != nil {
			return fmt.Errorf("could not save the raised budget: %w", err)
		}
	}
	p.Decider.Resolve(id, decide.Answer{Continue: cont, Add: add, Channel: channel})
	return nil
}

// trackProgress stores the agent's plan state and saves a forecast whenever a step completes.
func (p *Proxy) trackProgress(run string, body []byte) {
	pr := progress.Parse(body)
	p.setRunState(run, pr.Current(), progress.LastTools(body, 3))
	if !pr.Found {
		return
	}
	if len(pr.Steps) > 0 {
		rows := make([]ledger.StepRow, len(pr.Steps))
		for i, s := range pr.Steps {
			rows[i] = ledger.StepRow{Text: s.Text, Status: s.Status}
		}
		if err := p.Ledger.SetPlan(run, rows); err != nil {
			log.Printf("pitstop: plan write failed: %v", err)
		}
	}
	changed, err := p.Ledger.SetProgress(run, pr.Done, pr.Total)
	if err != nil || !changed {
		return
	}
	spent, _ := p.Ledger.SpentRun(run)
	costs, _ := p.Ledger.CallCosts(run)
	if f := forecast.ComputeWith(p.band(), spent, costs, pr.Done, pr.Total); f.HasPlan {
		_ = p.Ledger.SaveForecast(run, spent, f, pr.Done, pr.Total)
	}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// /run/<id>/... carries the run id in the base URL, so any tool with a base-URL setting works.
	if r.Method == http.MethodGet && r.URL.Path == "/" && r.Header.Get("Authorization") == "" && r.Header.Get("X-Api-Key") == "" {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}
	if r.URL.Path == "/dashboard" || strings.HasPrefix(r.URL.Path, "/dashboard/") {
		Dashboard(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/pitstop/") {
		p.Control(w, r)
		return
	}
	runID := ""
	if rest, ok := strings.CutPrefix(r.URL.Path, "/run/"); ok {
		id, path, _ := strings.Cut(rest, "/")
		runID, r.URL.Path = id, "/"+path
	}
	var provider string // API format: "anthropic" or "openai"
	var target *url.URL
	label, pname := "", "" // ledger label; named provider
	var named *Provider
	if rest, ok := strings.CutPrefix(r.URL.Path, "/p/"); ok {
		name, sub, _ := strings.Cut(rest, "/")
		pr := p.Providers[name]
		if pr == nil {
			fail(w, "openai", 404, fmt.Sprintf("Pitstop has no provider named %q. Add it to providers.json in the Pitstop folder.", name))
			return
		}
		named, pname, label = pr, name, name
		r.URL.Path = "/" + sub
		path := strings.TrimSuffix(r.URL.Path, "/")
		if !pr.metered(r.Method, path) {
			if !free(r.Method, path) {
				log.Printf("pitstop: blocked unmetered %s /p/%s%s", r.Method, name, path)
				fail(w, pr.Kind, 400, fmt.Sprintf("Pitstop cannot meter %s %s on %s yet, so it is blocked.", r.Method, path, name))
				return
			}
			p.forward(w, r, pr.url, nil)
			return
		}
		provider, target = pr.Kind, pr.url
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case named != nil:
	case r.Method == http.MethodPost && path == "/v1/messages":
		provider, target = "anthropic", p.Anthropic
	case r.Method == http.MethodPost && (path == "/v1/chat/completions" || path == "/v1/responses"):
		provider, target = "openai", p.OpenAI
	default:
		// Non-metered paths go to the provider the credentials belong to,
		// so an Anthropic key never reaches OpenAI or the other way round.
		prov, to := "", (*url.URL)(nil)
		switch {
		case r.Header.Get("X-Api-Key") != "" || r.Header.Get("Anthropic-Version") != "":
			prov, to = "anthropic", p.Anthropic
		case strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "):
			prov, to = "openai", p.OpenAI
		default:
			http.NotFound(w, r)
			return
		}
		if !free(r.Method, path) {
			// Fail closed: a call Pitstop cannot price must not go out unmetered.
			log.Printf("pitstop: blocked unmetered %s %s", r.Method, path)
			fail(w, prov, 400, fmt.Sprintf("Pitstop cannot meter %s %s yet, so it is blocked. Metered: POST /v1/messages, /v1/chat/completions, /v1/responses.", r.Method, path))
			return
		}
		p.forward(w, r, to, nil)
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
		Metadata  struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Model == "" {
		fail(w, provider, 400, "request has no JSON model field")
		return
	}
	if err := p.brokenErr(); err != nil {
		fail(w, provider, 503, "Pitstop blocked this call: its ledger could not be written ("+err.Error()+"), so spend is not being tracked. Fix: check free disk space and permissions on the ledger file, then restart pitstop serve. Run pitstop doctor to check.")
		return
	}
	entry, err := p.lookupFor(pname, named, req.Model)
	if err != nil {
		fail(w, provider, 400, err.Error()) // unknown price: block with a fix
		return
	}
	maxOut := firstPositive(req.MaxTokens, req.MaxComp, req.MaxOut, defaultMaxOutput)
	// High-side estimate: text bytes/3, plus a fixed allowance per image and PDF page.
	need := entry.MaxCost(estimateInputTokens(body), maxOut)

	// Only metadata.user_id: a session id quoted in the conversation must not win.
	session := ""
	if m := sessionRe.FindStringSubmatch(req.Metadata.UserID); provider == "anthropic" && m != nil {
		session = m[1]
	}
	run := runID
	if run == "" {
		run = r.Header.Get("X-Pitstop-Run")
	}
	if run == "" {
		run = p.DefaultRun
	}
	switch {
	case run != "":
	case session != "":
		run = SessionRun(session) // one run per Claude Code session, with its own run budget
	default:
		// No run identity at all (scripts, SDKs): day and month caps only, no run cap,
		// so a whole day of untagged calls is not squeezed into one run budget.
		run = caps.Untagged + time.Now().Format("20060102")
	}
	if session != "" && runID != "" {
		_ = p.Ledger.SetSession(run, session) // lets resume reopen the same Claude Code session
	}
	if provider == "anthropic" {
		p.trackProgress(run, body)
	} else {
		p.setRunState(run, "", progress.LastTools(body, 3))
	}
	step, _ := p.runState(run) // the call's cost belongs to the step in progress now
	if lr := loop.Detect(body); lr.Count >= loop.Warn {
		action := "warned"
		if lr.Count >= loop.Stop {
			action = "stopped"
		}
		if err := p.Ledger.RecordLoop(run, lr.Kind, lr.Tool, lr.Count, action); err != nil {
			log.Printf("pitstop: loop alert write failed: %v", err)
		}
		if action == "stopped" {
			fail(w, provider, 403, fmt.Sprintf("Pitstop stopped a loop: %s %q repeated %d times.", lr.Kind, lr.Tool, lr.Count)+p.save(run, "loop"))
			return
		}
	}
	if msg := p.refuse(run, need); msg != "" {
		fail(w, provider, 403, msg)
		return
	}
	defer p.Guard.Release(run, need)

	if provider == "openai" && req.Stream && strings.HasSuffix(path, "/chat/completions") {
		body = withUsageStreaming(body)
	}
	start := time.Now()
	m := &meter{provider: provider, stream: req.Stream}
	aborted := p.forwardCatch(w, r, target, &tap{body: body, m: m})
	u, estimated := m.usage(), false
	if aborted && m.billable {
		// Client hung up or the upstream dropped mid-response: the provider still bills.
		u, estimated = m.cutUsage(int64(len(body)))
	}
	if u != (price.Usage{}) { // zero: upstream error or no usage reported, nothing to bill
		if err := p.Ledger.Record(ledger.Call{
			RunID: run, Provider: cmp.Or(label, provider), Model: req.Model,
			Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, CacheWrite1h: u.CacheWrite1h,
			CostMicro: entry.Cost(u), LatencyMS: time.Since(start).Milliseconds(), At: start, Estimated: estimated, Step: step,
		}); err != nil {
			// Fail closed: spend is no longer tracked, so no further call may go out.
			p.setBroken(err)
			log.Printf("pitstop: ledger write failed, blocking all calls: %v", err)
		}
	}
	if aborted {
		panic(http.ErrAbortHandler) // keep the connection aborted, as ReverseProxy intended
	}
}

// forwardCatch forwards and reports whether the response was cut off mid-stream.
// ReverseProxy signals that by panicking with http.ErrAbortHandler, which would
// otherwise skip the ledger write.
func (p *Proxy) forwardCatch(w http.ResponseWriter, r *http.Request, target *url.URL, t *tap) (aborted bool) {
	defer func() {
		if v := recover(); v != nil {
			if v != http.ErrAbortHandler {
				panic(v)
			}
			aborted = true
		}
	}()
	p.forward(w, r, target, t)
	return false
}

func (p *Proxy) setBroken(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.broken == nil {
		p.broken = err
	}
}

// Broken is the ledger write error that is blocking all calls, or nil (for /healthz and doctor).
func (p *Proxy) Broken() error { return p.brokenErr() }

func (p *Proxy) brokenErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.broken
}

// free reports whether a non-metered request costs nothing: reads, deletes, token
// counting and cancels. Any other POST (batches, embeddings, images, audio, legacy
// completions) may cost money that Pitstop cannot price, so it is blocked.
func free(method, path string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodDelete:
		return true
	case http.MethodPost:
		return path == "/v1/messages/count_tokens" || path == "/v1/responses/input_tokens" ||
			strings.HasSuffix(path, "/cancel")
	}
	return false
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
				t.m.billable = true
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

// fail answers in the provider's error shape. Blocks use 403 because SDKs retry 429,
// which would re-run the checks and reopen a pause card on every retry.
func fail(w http.ResponseWriter, provider string, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if provider == "anthropic" {
		typ := "invalid_request_error"
		if code == http.StatusForbidden {
			typ = "permission_error"
		}
		fmt.Fprintf(w, `{"type":"error","error":{"type":%q,"message":%q}}`, typ, msg)
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
