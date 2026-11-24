package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/decide"
)

// fakeTG is a minimal Bot API: records calls, serves queued updates once.
type fakeTG struct {
	mu      sync.Mutex
	calls   []string         // method names
	bodies  []map[string]any // request bodies
	updates []any
	answers []string // answerCallbackQuery texts
}

func (f *fakeTG) srv(t *testing.T) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/botSECRET-TOKEN/") {
			t.Errorf("bad path %s", r.URL.Path)
		}
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		f.mu.Lock()
		f.calls, f.bodies = append(f.calls, method), append(f.bodies, body)
		var result any = true
		switch method {
		case "sendMessage":
			result = map[string]any{"message_id": 77}
		case "getUpdates":
			result, f.updates = f.updates, nil
			if result == nil {
				result = []any{}
			}
		case "answerCallbackQuery":
			f.answers = append(f.answers, body["text"].(string))
		case "getMe":
			result = map[string]any{"username": "pitstop_test_bot"}
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	t.Cleanup(s.Close)
	return s
}

func press(id int64, chat int64, data string) map[string]any {
	return map[string]any{"update_id": id, "callback_query": map[string]any{
		"id": "cb", "data": data, "message": map[string]any{"chat": map[string]any{"id": chat}}}}
}

func TestTelegramCardButtonsAndAnswers(t *testing.T) {
	f := &fakeTG{}
	s := f.srv(t)
	var answered []string
	tg := &Telegram{API: s.URL, Token: "SECRET-TOKEN", ChatID: 42, Answer: func(id string, cont bool, add int64) error {
		answered = append(answered, id)
		if id == "gone" {
			return errors.New("no such pending card")
		}
		return nil
	}}
	c := decide.Card{ID: "ab12", Run: "r1", Reason: "cap", Spent: 4_800_000, Budget: 5_000_000, Offer: 5_000_000}
	if err := tg.Card(c); err != nil {
		t.Fatal(err)
	}
	kb, _ := json.Marshal(f.bodies[0]["reply_markup"])
	if !strings.Contains(string(kb), "Continue +$5.00") || !strings.Contains(string(kb), "Stop \\u0026 save") {
		t.Fatalf("buttons: %s", kb)
	}
	if !strings.Contains(f.bodies[0]["text"].(string), "$4.80 of $5.00") {
		t.Fatalf("card text: %v", f.bodies[0]["text"])
	}
	n := tg.cards["ab12"].nonce

	ctx := context.Background()
	tg.handle(ctx, "x", "c:ab12:"+n, chat(999)) // a stranger
	tg.handle(ctx, "x", "c:ab12:wrongnonce", chat(42))
	if len(answered) != 0 {
		t.Fatal("stranger or bad nonce must not answer")
	}
	tg.handle(ctx, "x", "c:ab12:"+n, chat(42)) // the owner
	if len(answered) != 1 {
		t.Fatal("owner's Continue not passed on")
	}
	tg.handle(ctx, "x", "s:ab12:"+n, chat(42)) // same buttons again
	if len(answered) != 1 {
		t.Fatal("nonce reuse must be rejected")
	}
	// The card closes everywhere: Telegram edits it and drops the buttons.
	tg.Resolved(c, decide.Answer{Continue: true, Add: 5_000_000, Channel: "telegram"}, "Continued +$5.00 (answered in telegram).")
	if f.calls[len(f.calls)-1] != "editMessageText" {
		t.Fatalf("calls %v", f.calls)
	}
	if !strings.Contains(strings.Join(f.answers, "|"), "Not allowed") || !strings.Contains(strings.Join(f.answers, "|"), "no longer valid") {
		t.Fatalf("answers %v", f.answers)
	}
}

func TestTelegramLateAnswerAfterTimeout(t *testing.T) {
	f := &fakeTG{}
	s := f.srv(t)
	called := false
	tg := &Telegram{API: s.URL, Token: "SECRET-TOKEN", ChatID: 42, Answer: func(string, bool, int64) error { called = true; return nil }}
	c := decide.Card{ID: "cd34", Run: "r2", Offer: 1}
	tg.Card(c)
	n := tg.cards["cd34"].nonce
	tg.Resolved(c, decide.Answer{TimedOut: true, Channel: "timeout"}, Outcome(c, decide.Answer{TimedOut: true}, 9*time.Minute))
	tg.handle(context.Background(), "x", "c:cd34:"+n, chat(42))
	if called {
		t.Fatal("a late yes after the timeout must not continue the run")
	}
	if a := f.answers[len(f.answers)-1]; !strings.Contains(a, "Stopped after 9 minutes") || !strings.Contains(a, "pitstop resume r2") {
		t.Fatalf("late answer reply %q", a)
	}
}

func TestTelegramPollRoutesPresses(t *testing.T) {
	f := &fakeTG{}
	s := f.srv(t)
	got := make(chan string, 1)
	tg := &Telegram{API: s.URL, Token: "SECRET-TOKEN", ChatID: 42, Answer: func(id string, cont bool, add int64) error {
		got <- id
		return nil
	}}
	tg.Card(decide.Card{ID: "ef56", Run: "r3", Offer: 2})
	f.mu.Lock()
	f.updates = []any{press(10, 42, "s:ef56:"+tg.cards["ef56"].nonce)}
	f.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tg.Poll(ctx)
	select {
	case id := <-got:
		if id != "ef56" {
			t.Fatal(id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("button press never reached Answer")
	}
}

func TestTelegramErrorsNeverShowToken(t *testing.T) {
	tg := &Telegram{API: "http://127.0.0.1:1", Token: "SECRET-TOKEN", ChatID: 1}
	err := tg.Send(context.Background(), "hi")
	if err == nil || strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Fatalf("error leaks token or no error: %v", err)
	}
}

func TestTelegramSetupHelpers(t *testing.T) {
	f := &fakeTG{}
	s := f.srv(t)
	f.updates = []any{map[string]any{"update_id": 5, "message": map[string]any{"chat": map[string]any{"id": 4242}}}}
	tg := &Telegram{API: s.URL, Token: "SECRET-TOKEN"}
	if n, err := tg.BotName(context.Background()); err != nil || n != "pitstop_test_bot" {
		t.Fatal(n, err)
	}
	if id, err := tg.FirstChat(context.Background()); err != nil || id != 4242 {
		t.Fatal(id, err)
	}
}

func chat(id int64) *struct {
	Chat struct {
		ID int64 `json:"id"`
	} `json:"chat"`
} {
	m := &struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	}{}
	m.Chat.ID = id
	return m
}
