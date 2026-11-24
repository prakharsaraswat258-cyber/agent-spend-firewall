package notify

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/decide"
)

type rec struct {
	mu                    sync.Mutex
	cards, reminds, ended []string
}

func (r *rec) Name() string { return "rec" }
func (r *rec) Card(c decide.Card) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cards = append(r.cards, c.ID)
	return nil
}
func (r *rec) Remind(c decide.Card) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reminds = append(r.reminds, c.ID)
	return nil
}
func (r *rec) Resolved(c decide.Card, a decide.Answer, out string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ended = append(r.ended, out)
	return nil
}
func (r *rec) snap() (int, int, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cards), len(r.reminds), append([]string(nil), r.ended...)
}

func TestHubFansOutRemindsAndCloses(t *testing.T) {
	pending := map[string]bool{"a": true}
	var mu sync.Mutex
	h := NewHub(time.Second, func(id string) bool { mu.Lock(); defer mu.Unlock(); return pending[id] })
	h.RemindAfter = 50 * time.Millisecond
	r1, r2 := &rec{}, &rec{}
	h.Add(r1)
	h.Add(r2)
	h.Card(decide.Card{ID: "a", Run: "r"})
	time.Sleep(150 * time.Millisecond)
	for _, r := range []*rec{r1, r2} {
		if c, m, _ := r.snap(); c != 1 || m != 1 {
			t.Fatalf("card %d remind %d", c, m)
		}
	}
	// An answered card gets no reminder.
	mu.Lock()
	pending["b"] = false
	mu.Unlock()
	h.Card(decide.Card{ID: "b", Run: "r"})
	time.Sleep(150 * time.Millisecond)
	if _, m, _ := r1.snap(); m != 1 {
		t.Fatal("answered card reminded")
	}
	h.Resolved(decide.Card{ID: "a", Run: "r"}, decide.Answer{Continue: true, Add: 9_000_000, Channel: "terminal"})
	if _, _, e := r2.snap(); len(e) != 1 || e[0] != "Continued +$9.00 (answered in terminal)." {
		t.Fatalf("%v", e)
	}
}

func TestOutcomeLines(t *testing.T) {
	c := decide.Card{Run: "ab12"}
	if o := Outcome(c, decide.Answer{TimedOut: true}, 9*time.Minute); o != "Stopped after 9 minutes with no answer. Work saved. Resume with: pitstop resume ab12" {
		t.Fatal(o)
	}
	if o := Outcome(c, decide.Answer{Channel: "dashboard"}, time.Minute); !strings.HasPrefix(o, "Stopped (answered in dashboard)") {
		t.Fatal(o)
	}
}

func TestToastXMLIsEscapedAndClickable(t *testing.T) {
	x := ToastXML("Pitstop: <paused>", "Run a&b will not fit.\nline2\nline3", "http://127.0.0.1:4546/dashboard?x=1&y=2", true)
	for _, want := range []string{`launch="http://127.0.0.1:4546/dashboard?x=1&amp;y=2"`, "&lt;paused&gt;", "a&amp;b", `content="Open decision"`, "line2 line3"} {
		if !strings.Contains(x, want) {
			t.Fatalf("missing %q in %s", want, x)
		}
	}
	var got string
	tt := Toast{URL: "http://127.0.0.1:4546/dashboard", Run: func(s string) error { got = s; return nil }}
	tt.Card(decide.Card{Run: "r", Reason: "cap", Spent: 1, Budget: 2, Offer: 1})
	if !strings.Contains(got, "CreateToastNotifier") || !strings.Contains(got, "Budget reached on run r") {
		t.Fatalf("script %s", got)
	}
	// Only a timeout gets a closing toast; an answer the user gave needs none.
	got = ""
	tt.Resolved(decide.Card{Run: "r"}, decide.Answer{Continue: true}, "x")
	if got != "" {
		t.Fatal("toast after the user's own answer")
	}
}

// Shows a real Windows notification. Run by hand: PITSTOP_LIVE_TOAST=1 go test -run Live ./internal/notify
func TestLiveToast(t *testing.T) {
	if os.Getenv("PITSTOP_LIVE_TOAST") != "1" {
		t.Skip("set PITSTOP_LIVE_TOAST=1 to show a real notification")
	}
	err := Toast{URL: "http://127.0.0.1:4546/dashboard"}.Card(decide.Card{Run: "demo", Reason: "cap", Spent: 4_800_000, Budget: 5_000_000, Offer: 5_000_000})
	if err != nil {
		t.Fatal(err)
	}
}
