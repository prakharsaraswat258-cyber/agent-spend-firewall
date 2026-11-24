// Package notify sends decision cards to where the developer is: the desktop and,
// optionally, a phone (Telegram). Every channel shows the same card, any channel
// can answer, and the others are updated once one does. Nothing auto-continues.
package notify

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/decide"
)

// Channel is one place a card can be shown.
type Channel interface {
	Name() string
	// Card shows a new card.
	Card(c decide.Card) error
	// Remind nudges about a card still waiting.
	Remind(c decide.Card) error
	// Resolved closes a card; outcome is one line such as "Continued +$9.00 (answered in terminal)".
	Resolved(c decide.Card, a decide.Answer, outcome string) error
}

// Hub fans cards out to every channel.
type Hub struct {
	Timeout     time.Duration // the pause timeout, for the "stopped after" line
	RemindAfter time.Duration // 0: no reminder
	Pending     func(id string) bool

	mu       sync.Mutex
	channels []Channel
	timers   map[string]*time.Timer
}

func NewHub(timeout time.Duration, pending func(id string) bool) *Hub {
	return &Hub{Timeout: timeout, RemindAfter: 5 * time.Minute, Pending: pending, timers: map[string]*time.Timer{}}
}

func (h *Hub) Add(c Channel) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.channels = append(h.channels, c)
}

func (h *Hub) list() []Channel {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Channel(nil), h.channels...)
}

// Card is decide.Decider.Notify: show the card everywhere, then remind once if it waits.
func (h *Hub) Card(c decide.Card) {
	for _, ch := range h.list() {
		go func() { // a blocking dialog on one channel must not delay the others
			if err := ch.Card(c); err != nil {
				log.Printf("pitstop: %s notification failed: %v", ch.Name(), err)
			}
		}()
	}
	if h.RemindAfter <= 0 || h.RemindAfter >= h.Timeout {
		return
	}
	t := time.AfterFunc(h.RemindAfter, func() {
		if h.Pending != nil && !h.Pending(c.ID) {
			return
		}
		for _, ch := range h.list() {
			if err := ch.Remind(c); err != nil {
				log.Printf("pitstop: %s reminder failed: %v", ch.Name(), err)
			}
		}
	})
	h.mu.Lock()
	h.timers[c.ID] = t
	h.mu.Unlock()
}

// Resolved is called once per card when it is answered or times out.
func (h *Hub) Resolved(c decide.Card, a decide.Answer) {
	h.mu.Lock()
	if t := h.timers[c.ID]; t != nil {
		t.Stop()
		delete(h.timers, c.ID)
	}
	h.mu.Unlock()
	out := Outcome(c, a, h.Timeout)
	for _, ch := range h.list() {
		if err := ch.Resolved(c, a, out); err != nil {
			log.Printf("pitstop: %s update failed: %v", ch.Name(), err)
		}
	}
}

// Outcome is the one line every channel shows once a card is closed.
func Outcome(c decide.Card, a decide.Answer, timeout time.Duration) string {
	switch {
	case a.Continue:
		return fmt.Sprintf("Continued +%s (answered in %s).", decide.USD(a.Add), a.Channel)
	case a.TimedOut:
		return fmt.Sprintf("Stopped after %d minutes with no answer. Work saved. Resume with: pitstop resume %s", int(timeout.Minutes()), c.Run)
	default:
		return fmt.Sprintf("Stopped (answered in %s). Work saved. Resume with: pitstop resume %s", a.Channel, c.Run)
	}
}

// Func adapts a plain function (e.g. a blocking macOS/Linux dialog) into a Channel
// that only shows new cards.
type Func struct {
	N string
	F func(decide.Card)
}

func (f Func) Name() string                                      { return f.N }
func (f Func) Card(c decide.Card) error                          { f.F(c); return nil }
func (f Func) Remind(decide.Card) error                          { return nil }
func (f Func) Resolved(decide.Card, decide.Answer, string) error { return nil }
