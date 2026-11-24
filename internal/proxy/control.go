package proxy

import (
	_ "embed"
	"time"

	"encoding/json"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/forecast"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/ledger"
	"net"
	"net/http"
	"strings"
)

// Control serves /pitstop/pending and /pitstop/answer for the CLI and dashboard.
// Guards against web pages poking localhost: loopback Host only (DNS rebinding)
// and a custom header (forces a CORS preflight, which is never granted).
func (p *Proxy) Control(w http.ResponseWriter, r *http.Request) {
	h, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		h = r.Host
	}
	if h != "127.0.0.1" && h != "localhost" && h != "[::1]" && h != "::1" {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	if r.Header.Get("X-Pitstop") != "1" {
		http.Error(w, "missing X-Pitstop header", http.StatusForbidden)
		return
	}
	if p.Decider == nil && !strings.HasSuffix(r.URL.Path, "/overview") && !strings.HasSuffix(r.URL.Path, "/calls") {
		http.Error(w, "decision cards are off", http.StatusNotFound)
		return
	}
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/overview"):
		p.overview(w)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/calls"):
		cs, _ := p.Ledger.Calls(r.URL.Query().Get("run"), 100)
		json.NewEncoder(w).Encode(cs)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pending"):
		json.NewEncoder(w).Encode(p.Decider.Pending())
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/answer"):
		var a struct {
			ID       string `json:"id"`
			Continue bool   `json:"continue"`
			AddMicro int64  `json:"add_micro"`
			Channel  string `json:"channel"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&a) != nil || a.AddMicro < 0 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := p.Respond(a.ID, a.Continue, a.AddMicro, firstNonEmpty(a.Channel, "cli")); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Write([]byte("ok"))
	default:
		http.NotFound(w, r)
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

//go:embed dashboard.html
var dashboardHTML []byte

// Dashboard serves the static page. It holds no data: the page calls /pitstop/* with the guard header.
func Dashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'")
	w.Write(dashboardHTML)
}

type runView struct {
	ID, Repo, Branch, Agent, Status string
	Spent, Budget, P50, P90         int64
	Done, Total, Calls, Loops       int
	Confidence                      string
}

func (p *Proxy) overview(w http.ResponseWriter) {
	now := time.Now()
	day, _ := p.Ledger.SpentSince(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()))
	month, _ := p.Ledger.SpentSince(time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()))
	waiting := map[string]bool{}
	var cards []any
	if p.Decider != nil {
		for _, c := range p.Decider.Pending() {
			waiting[c.Run] = true
			cards = append(cards, c)
		}
	}
	acc, _ := p.Ledger.Accuracy()
	rs, _ := p.Ledger.Runs(50)
	var views []runView
	for _, r := range rs {
		v := runView{ID: r.ID, Repo: r.Repo, Branch: r.Branch, Agent: r.Agent, Spent: r.CostMicro, Budget: p.Guard.RunCap(r.ID),
			Done: r.StepsDone, Total: r.StepsTotal, Calls: int(r.Calls), Loops: int(r.Loops), Status: "idle"}
		if time.Since(r.LastCall) < 2*time.Minute {
			v.Status = "running"
		}
		if r.Loops > 0 {
			v.Status = "looping"
		}
		if waiting[r.ID] {
			v.Status = "needs you"
		}
		if r.StepsTotal > 0 {
			costs, _ := p.Ledger.CallCosts(r.ID)
			f := forecast.Compute(r.CostMicro, costs, r.StepsDone, r.StepsTotal)
			v.P50, v.P90, v.Confidence = f.P50, f.P90, f.Confidence
		}
		views = append(views, v)
	}
	json.NewEncoder(w).Encode(map[string]any{"day": day, "month": month, "dayCap": p.Guard.L.Day, "monthCap": p.Guard.L.Month, "accHits": acc.Hits, "accScored": acc.Scored, "accMin": ledger.MinScored, "cards": cards, "runs": views})
}
