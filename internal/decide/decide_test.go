package decide

import (
	"sync"
	"testing"
	"time"
)

func TestTimeoutStops(t *testing.T) {
	d := New(30 * time.Millisecond)
	if a := d.Ask(Card{Run: "r"}); a.Continue || !a.TimedOut {
		t.Fatalf("%+v", a)
	}
	if len(d.Pending()) != 0 {
		t.Fatal("card left behind")
	}
}

func TestConcurrentAsksShareOneCard(t *testing.T) {
	d := New(5 * time.Second)
	var wg sync.WaitGroup
	got := make([]Answer, 3)
	for i := range got {
		wg.Add(1)
		go func(i int) { defer wg.Done(); got[i] = d.Ask(Card{Run: "r"}) }(i)
	}
	var id string
	for id == "" {
		time.Sleep(5 * time.Millisecond)
		if p := d.Pending(); len(p) == 1 {
			id = p[0].ID
		}
	}
	time.Sleep(20 * time.Millisecond)
	if len(d.Pending()) != 1 {
		t.Fatal("want one card")
	}
	if !d.Resolve(id, Answer{Continue: true, Add: 7}) || d.Resolve(id, Answer{}) {
		t.Fatal("resolve once only")
	}
	wg.Wait()
	for _, a := range got {
		if !a.Continue || a.Add != 7 {
			t.Fatalf("%+v", a)
		}
	}
}
