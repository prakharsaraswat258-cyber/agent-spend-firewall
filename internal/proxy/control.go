package proxy

import (
	"encoding/json"
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
	if p.Decider == nil {
		http.Error(w, "decision cards are off", http.StatusNotFound)
		return
	}
	switch {
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
