// Package decide holds a run at a pause point until a human answers.
// Never auto-continues: no answer in Timeout means stop.
package decide

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Card is what the human sees. Money in micro-dollars.
type Card struct {
	ID, Run    string
	Reason     string // "forecast" or "cap"
	Spent      int64
	Budget     int64
	P50, P90   int64 // still to spend; zero when there is no plan
	Offer      int64 // suggested budget raise
	Confidence string
	Done       int
	Total      int
	Asked      time.Time
}

type Answer struct {
	Continue bool
	Add      int64 // budget raise, micro-dollars
	TimedOut bool
	Channel  string // "cli", "desktop", "timeout"
}

type pending struct {
	card Card
	done chan struct{}
	ans  Answer
}

type Decider struct {
	Timeout time.Duration
	Notify  func(Card)                        // optional: desktop popup etc. Called in its own goroutine.
	Log     func(Card, Answer, time.Duration) // optional: called once per card when resolved
	mu      sync.Mutex
	byRun   map[string]*pending
	byID    map[string]*pending
}

func New(timeout time.Duration) *Decider {
	return &Decider{Timeout: timeout, byRun: map[string]*pending{}, byID: map[string]*pending{}}
}

// Ask blocks until answered or timed out. Concurrent calls for one run share one card.
func (d *Decider) Ask(c Card) Answer {
	d.mu.Lock()
	p := d.byRun[c.Run]
	if p == nil {
		b := make([]byte, 4)
		rand.Read(b)
		c.ID, c.Asked = hex.EncodeToString(b), time.Now()
		p = &pending{card: c, done: make(chan struct{})}
		d.byRun[c.Run], d.byID[c.ID] = p, p
		if d.Notify != nil {
			go d.Notify(c)
		}
		time.AfterFunc(d.Timeout, func() { d.Resolve(c.ID, Answer{TimedOut: true, Channel: "timeout"}) })
	}
	d.mu.Unlock()
	<-p.done
	return p.ans
}

// Resolve answers a card. False if it is unknown or already answered.
func (d *Decider) Resolve(id string, a Answer) bool {
	d.mu.Lock()
	p := d.byID[id]
	if p == nil {
		d.mu.Unlock()
		return false
	}
	delete(d.byID, id)
	delete(d.byRun, p.card.Run)
	p.ans = a
	d.mu.Unlock()
	if d.Log != nil {
		d.Log(p.card, a, time.Since(p.card.Asked))
	}
	close(p.done)
	return true
}

func (d *Decider) Pending() []Card {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []Card
	for _, p := range d.byID {
		out = append(out, p.card)
	}
	return out
}

// Card returns a pending card by id.
func (d *Decider) Card(id string) (Card, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if p := d.byID[id]; p != nil {
		return p.card, true
	}
	return Card{}, false
}
