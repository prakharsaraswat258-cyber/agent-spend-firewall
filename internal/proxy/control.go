package proxy

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Control serves the JSON API for the CLI, the PreToolUse hook and the dashboard.
// Guards against web pages poking localhost:
//   - loopback Host only (blocks DNS rebinding);
//   - a custom X-Pitstop header (forces a CORS preflight, which is never granted);
//   - a request that carries an Origin must come from this server's own origin.
//
// The live event stream can't send custom headers (EventSource), so it relies on
// the Host and Origin checks; it only reads, and a cross-site page can't read it
// without CORS headers, which are never sent.
func (p *Proxy) Control(w http.ResponseWriter, r *http.Request) {
	if !loopbackHost(r.Host) {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	if o := r.Header.Get("Origin"); o != "" {
		if u, err := url.Parse(o); err != nil || u.Host != r.Host {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
	}
	events := r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/events")
	if !events && r.Header.Get("X-Pitstop") != "1" {
		http.Error(w, "missing X-Pitstop header", http.StatusForbidden)
		return
	}
	cardsOnly := strings.HasSuffix(r.URL.Path, "/pending") || strings.HasSuffix(r.URL.Path, "/answer")
	if p.Decider == nil && cardsOnly {
		http.Error(w, "decision cards are off", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()
	switch {
	case events:
		p.events(w, r)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/overview"):
		writeJSON(w, p.overviewData())
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/run"):
		d, ok := p.runDetail(q.Get("id"))
		if !ok {
			http.Error(w, "no such run", http.StatusNotFound)
			return
		}
		writeJSON(w, d)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/history"):
		writeJSON(w, p.historyData(q.Get("repo"), q.Get("agent")))
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/plan"):
		steps, _ := p.Ledger.Plan(q.Get("run"))
		writeJSON(w, steps)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/calls"):
		cs, _ := p.Ledger.Calls(q.Get("run"), 100)
		writeJSON(w, cs)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/gate"):
		// Called by `pitstop hook pre-tool`; may wait for a human answer (up to the pause timeout).
		var g struct {
			Run     string `json:"run"`
			Session string `json:"session"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&g) != nil || g.Run == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if sessionID.MatchString(g.Session) {
			_ = p.Ledger.SetSession(g.Run, g.Session)
		}
		msg := p.Gate(g.Run)
		writeJSON(w, map[string]any{"allow": msg == "", "message": msg})
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pending"):
		writeJSON(w, p.Decider.Pending())
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/answer"):
		var a struct {
			ID       string `json:"id"`
			Continue bool   `json:"continue"`
			AddMicro int64  `json:"add_micro"`
			Channel  string `json:"channel"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&a) != nil || a.AddMicro < 0 || (a.Continue && a.AddMicro == 0) {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := p.Respond(a.ID, a.Continue, a.AddMicro, firstNonEmpty(a.Channel, "cli")); err != nil {
			// First answer wins: a second one (another channel, or after the timeout) is refused.
			http.Error(w, "already answered or timed out", http.StatusConflict)
			return
		}
		w.Write([]byte("ok"))
	default:
		http.NotFound(w, r)
	}
}

func loopbackHost(hostport string) bool {
	h, _, err := net.SplitHostPort(hostport)
	if err != nil {
		h = hostport
	}
	h = strings.Trim(h, "[]")
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// events streams the overview to the dashboard (Server-Sent Events): a frame
// whenever it changes, checked every second, and a keep-alive comment otherwise.
func (p *Proxy) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var last []byte
	quiet := 0
	for {
		b, _ := json.Marshal(p.overviewData())
		if string(b) != string(last) {
			w.Write([]byte("data: "))
			w.Write(b)
			w.Write([]byte("\n\n"))
			fl.Flush()
			last, quiet = b, 0
		} else if quiet++; quiet >= 15 {
			w.Write([]byte(": keep-alive\n\n"))
			fl.Flush()
			quiet = 0
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}

// sessionID: agent session ids are UUIDs; anything else is ignored.
var sessionID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
