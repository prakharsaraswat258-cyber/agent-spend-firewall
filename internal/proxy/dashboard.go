package proxy

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/decide"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/forecast"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/ledger"
)

// The dashboard is plain HTML, CSS and JS built into the binary. It makes no
// external requests: fonts are served from here, and the CSP allows only 'self'.
//
//go:embed web
var webFS embed.FS

const csp = "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Dashboard serves /dashboard and its files under /dashboard/.
func Dashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !loopbackHost(r.Host) {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	name := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/dashboard"), "/")
	if name == "" {
		name = "index.html"
	}
	name = path.Clean(name)
	if strings.HasPrefix(name, "..") {
		http.NotFound(w, r)
		return
	}
	b, err := fs.ReadFile(webFS, "web/"+name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	types := map[string]string{".html": "text/html; charset=utf-8", ".css": "text/css; charset=utf-8",
		".js": "text/javascript; charset=utf-8", ".svg": "image/svg+xml", ".woff2": "font/woff2", ".txt": "text/plain; charset=utf-8"}
	w.Header().Set("Content-Type", types[path.Ext(name)])
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-cache") // revalidate, so an upgraded binary's page is used at once
	w.Write(b)
}

// RunView is one run row on the Live screen.
type RunView struct {
	ID, Repo, Branch, Agent string
	Status                  string // running, idle, needs you, looping, stopped, finished, ended
	Fit                     string // on track, tight, won't fit, no run cap, "" (no forecast)
	Spent, Budget           int64
	P10, P50, P90           int64
	Done, Total, Calls      int
	Loops                   int
	Confidence, Method      string
	LastCall                int64
}

type Overview struct {
	Day, Month, DayCap, MonthCap int64
	AccHits, AccScored, AccMin   int
	AccUnder, AccOver            int
	TimeoutSec                   int
	Cards                        []decide.Card
	Recent                       []ledger.DecisionRow // answered in the last 2 minutes
	Runs                         []RunView
}

func fitLabel(spent, budget int64, f forecast.Forecast) string {
	switch {
	case !f.Valid():
		return ""
	case budget <= 0:
		return "no run cap"
	case forecast.WillOverrun(spent, budget, f):
		return "won't fit"
	case spent+f.P90 > budget:
		return "tight"
	}
	return "on track"
}

func (p *Proxy) runView(r ledger.Run, waiting bool) RunView {
	v := RunView{ID: r.ID, Repo: r.Repo, Branch: r.Branch, Agent: r.Agent, Spent: r.CostMicro, Budget: p.Guard.RunCap(r.ID),
		Done: r.StepsDone, Total: r.StepsTotal, Calls: int(r.Calls), Loops: int(r.Loops), Status: "idle", LastCall: r.LastCall.Unix()}
	if r.Calls == 0 {
		v.LastCall = 0
	}
	recent := r.Calls > 0 && time.Since(r.LastCall) < 2*time.Minute
	outcome, _ := p.Ledger.Outcome(r.ID)
	stopped, _ := p.Ledger.Stopped(r.ID)
	switch {
	case waiting:
		v.Status = "needs you"
	case stopped:
		v.Status = "stopped"
	case recent && r.Loops > 0 && p.recentLoop(r.ID):
		v.Status = "looping"
	case recent:
		v.Status = "running"
	case outcome != "":
		v.Status = outcome
	}
	// A finished run, or one whose plan is complete, has nothing left to forecast.
	done := outcome != "" || (r.StepsTotal > 0 && r.StepsDone >= r.StepsTotal)
	if f, _ := p.Forecast(r.ID, r.CostMicro); f.Valid() && !done {
		v.P10, v.P50, v.P90, v.Confidence, v.Method = f.P10, f.P50, f.P90, f.Confidence, f.Method
		v.Fit = fitLabel(r.CostMicro, v.Budget, f)
	}
	return v
}

func (p *Proxy) overviewData() Overview {
	now := time.Now()
	o := Overview{DayCap: p.Guard.L.Day, MonthCap: p.Guard.L.Month, AccMin: ledger.MinScored}
	o.Day, _ = p.Ledger.SpentSince(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()))
	o.Month, _ = p.Ledger.SpentSince(time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()))
	waiting := map[string]bool{}
	if p.Decider != nil {
		o.TimeoutSec = int(p.Decider.Timeout.Seconds())
		o.Cards = p.Decider.Pending()
		for _, c := range o.Cards {
			waiting[c.Run] = true
		}
	}
	o.Recent, _ = p.Ledger.Decisions("", now.Add(-2*time.Minute), 5)
	acc, _ := p.Ledger.Accuracy()
	o.AccHits, o.AccScored, o.AccUnder, o.AccOver = acc.Hits, acc.Scored, acc.Under, acc.Over
	rs, _ := p.Ledger.Runs(20)
	for _, r := range rs {
		o.Runs = append(o.Runs, p.runView(r, waiting[r.ID]))
	}
	return o
}

type RunDetail struct {
	RunView
	Dir, SessionID, Outcome string
	Started, Ended          int64
	Resume                  string
	Card                    *decide.Card
	Plan                    []ledger.PlanStep
	Series                  []ledger.SpendPoint
	Checkpoints             []ledger.CheckpointRow
	Decisions               []ledger.DecisionRow
	Loops                   []ledger.LoopRow
	CallList                []ledger.CallRow
}

func (p *Proxy) runDetail(id string) (RunDetail, bool) {
	r, err := p.Ledger.GetRun(id)
	if err != nil || id == "" {
		return RunDetail{}, false
	}
	var row ledger.Run
	if rs, _ := p.Ledger.Runs(1000); rs != nil {
		for _, x := range rs {
			if x.ID == id {
				row = x
			}
		}
	}
	row.ID, row.Repo, row.Branch, row.Agent = id, r.Repo, r.Branch, r.Agent
	row.StepsDone, row.StepsTotal = r.StepsDone, r.StepsTotal
	var card *decide.Card
	if p.Decider != nil {
		for _, c := range p.Decider.Pending() {
			if c.Run == id {
				c := c
				card = &c
			}
		}
	}
	d := RunDetail{RunView: p.runView(row, card != nil), Dir: r.Dir, SessionID: r.SessionID, Started: r.Started.Unix(),
		Resume: "pitstop resume " + id, Card: card}
	d.Outcome, d.Ended = p.Ledger.Outcome(id)
	d.Plan, _ = p.Ledger.Plan(id)
	d.Series, _ = p.Ledger.Series(id)
	d.Checkpoints, _ = p.Ledger.Checkpoints(id)
	d.Decisions, _ = p.Ledger.Decisions(id, time.Unix(0, 0), 50)
	d.Loops, _ = p.Ledger.LoopAlerts(id)
	d.CallList, _ = p.Ledger.Calls(id, 200)
	return d, true
}

type HistoryRow struct {
	ledger.HistoryRun
	Budget  int64
	Verdict string // "right", "too high", "too low", "" (not scored)
	P10     int64
	P90     int64
	Rem     int64
}

type History struct {
	Runs                       []HistoryRow
	Repos, Agents              []string
	AccHits, AccScored, AccMin int
	AccUnder, AccOver          int
}

func (p *Proxy) historyData(repo, agent string) History {
	acc, _ := p.Ledger.Accuracy()
	scored := map[string]ledger.AccRun{}
	for _, a := range acc.Runs {
		scored[a.ID] = a
	}
	h := History{AccHits: acc.Hits, AccScored: acc.Scored, AccMin: ledger.MinScored, AccUnder: acc.Under, AccOver: acc.Over}
	h.Repos, h.Agents = p.Ledger.Distinct()
	rs, _ := p.Ledger.History(repo, agent, 500)
	for _, r := range rs {
		row := HistoryRow{HistoryRun: r, Budget: p.Guard.RunCap(r.ID)}
		if a, ok := scored[r.ID]; ok {
			row.P10, row.P90, row.Rem = a.P10, a.P90, a.Rem
			switch {
			case a.Hit:
				row.Verdict = "right"
			case a.Under:
				row.Verdict = "too high"
			default:
				row.Verdict = "too low"
			}
		}
		h.Runs = append(h.Runs, row)
	}
	return h
}

// recentLoop: a loop alert in the last 5 minutes (older ones don't make a run "looping").
func (p *Proxy) recentLoop(run string) bool {
	ls, _ := p.Ledger.LoopAlerts(run)
	return len(ls) > 0 && time.Since(time.Unix(ls[0].At, 0)) < 5*time.Minute
}
