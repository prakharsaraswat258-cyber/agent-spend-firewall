package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/decide"
)

// Telegram is the optional phone channel (off by default). Cards arrive with
// Continue and Stop buttons; answers are read by long polling, so no public URL is
// needed. Only the stored chat may answer, and each card's buttons carry a random
// one-time nonce. The bot token stays in memory: never stored, never logged.
type Telegram struct {
	API    string // https://api.telegram.org; a fake server in tests
	Token  string
	ChatID int64
	// Answer resolves a card (Continue raises the budget by add). It returns an
	// error when the card is no longer open, e.g. it already timed out.
	Answer func(cardID string, cont bool, add int64) error
	Client *http.Client

	mu    sync.Mutex
	cards map[string]*tgCard
}

type tgCard struct {
	card   decide.Card
	nonce  string // "" once used
	msgID  int64
	closed string // outcome line once the card is closed
}

const TelegramAPI = "https://api.telegram.org"

func (t *Telegram) Name() string { return "telegram" }

// call posts one Bot API method. Errors never contain the token: Go's URL errors
// quote the whole request URL, which has the token in its path, so only the cause is kept.
func (t *Telegram) call(ctx context.Context, method string, in, out any) error {
	b, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(t.API, "/")+"/bot"+t.Token+"/"+method, bytes.NewReader(b))
	if err != nil {
		return errors.New("telegram: bad request")
	}
	req.Header.Set("Content-Type", "application/json")
	c := t.Client
	if c == nil {
		c = &http.Client{Timeout: 70 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("telegram %s: %v", method, err)
	}
	defer resp.Body.Close()
	var r struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("telegram %s: HTTP %d", method, resp.StatusCode)
	}
	if !r.OK {
		return fmt.Errorf("telegram %s: %s", method, r.Description)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func nonce() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func keyboard(c decide.Card, n string) map[string]any {
	return map[string]any{"inline_keyboard": [][]map[string]string{{
		{"text": "Continue +" + decide.USD(c.Offer), "callback_data": "c:" + c.ID + ":" + n},
		{"text": "Stop & save", "callback_data": "s:" + c.ID + ":" + n},
	}}}
}

func (t *Telegram) Card(c decide.Card) error {
	n := nonce()
	var m struct {
		ID int64 `json:"message_id"`
	}
	err := t.call(context.Background(), "sendMessage", map[string]any{
		"chat_id": t.ChatID, "text": "Pitstop: your agent is paused.\n\n" + c.Text() + "\nNo answer in time means it stops and saves your work.",
		"reply_markup": keyboard(c, n),
	}, &m)
	if err != nil {
		return err
	}
	t.mu.Lock()
	if t.cards == nil {
		t.cards = map[string]*tgCard{}
	}
	t.cards[c.ID] = &tgCard{card: c, nonce: n, msgID: m.ID}
	t.mu.Unlock()
	return nil
}

func (t *Telegram) Remind(c decide.Card) error {
	t.mu.Lock()
	tc := t.cards[c.ID]
	t.mu.Unlock()
	in := map[string]any{"chat_id": t.ChatID, "text": "Still waiting for your answer on run " + c.Run + "."}
	if tc != nil {
		in["reply_parameters"] = map[string]any{"message_id": tc.msgID}
	}
	return t.call(context.Background(), "sendMessage", in, nil)
}

// Resolved edits the card to show who answered (or that it timed out) and removes the buttons.
func (t *Telegram) Resolved(c decide.Card, a decide.Answer, outcome string) error {
	t.mu.Lock()
	tc := t.cards[c.ID]
	if tc != nil {
		tc.nonce, tc.closed = "", outcome
	}
	t.mu.Unlock()
	if tc == nil {
		return nil
	}
	return t.call(context.Background(), "editMessageText", map[string]any{
		"chat_id": t.ChatID, "message_id": tc.msgID, "text": "Pitstop\n\n" + c.Text() + "\n" + outcome,
	}, nil)
}

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message"`
	Callback *struct {
		ID      string `json:"id"`
		Data    string `json:"data"`
		Message *struct {
			Chat struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"message"`
	} `json:"callback_query"`
}

// Poll reads button presses until ctx ends.
func (t *Telegram) Poll(ctx context.Context) {
	var offset int64
	for ctx.Err() == nil {
		var ups []tgUpdate
		err := t.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 50, "allowed_updates": []string{"callback_query"}}, &ups)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("pitstop: %v (retrying in 5s)", err)
				select {
				case <-ctx.Done():
				case <-time.After(5 * time.Second):
				}
			}
			continue
		}
		for _, u := range ups {
			offset = max(offset, u.UpdateID+1)
			if u.Callback != nil {
				t.handle(ctx, u.Callback.ID, u.Callback.Data, u.Callback.Message)
			}
		}
	}
}

// handle answers one button press. Rejected: another chat, a used or wrong nonce,
// a card that is closed (answered elsewhere or timed out).
func (t *Telegram) handle(ctx context.Context, cbID, data string, msg *struct {
	Chat struct {
		ID int64 `json:"id"`
	} `json:"chat"`
}) {
	reply := func(text string) {
		if err := t.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": cbID, "text": text, "show_alert": true}, nil); err != nil {
			log.Printf("pitstop: %v", err)
		}
	}
	if msg == nil || msg.Chat.ID != t.ChatID {
		reply("Not allowed: this bot only takes answers from its owner's chat.")
		return
	}
	parts := strings.Split(data, ":")
	if len(parts) != 3 || (parts[0] != "c" && parts[0] != "s") {
		reply("Unknown button.")
		return
	}
	t.mu.Lock()
	tc := t.cards[parts[1]]
	switch {
	case tc == nil:
		t.mu.Unlock()
		reply("This card is no longer open.")
		return
	case tc.closed != "":
		closed := tc.closed
		t.mu.Unlock()
		reply("Already closed: " + closed)
		return
	case tc.nonce == "" || tc.nonce != parts[2]:
		t.mu.Unlock()
		reply("This button is no longer valid.")
		return
	}
	tc.nonce = "" // one-time
	c := tc.card
	t.mu.Unlock()
	cont := parts[0] == "c"
	add := int64(0)
	if cont {
		add = c.Offer
	}
	if err := t.Answer(c.ID, cont, add); err != nil {
		reply("This run already stopped. Resume with: pitstop resume " + c.Run)
		return
	}
	if cont {
		reply("Continued +" + decide.USD(add) + ".")
	} else {
		reply("Stopped. Work saved. Resume with: pitstop resume " + c.Run)
	}
}

// BotName checks the token and returns the bot's @username (for setup).
func (t *Telegram) BotName(ctx context.Context) (string, error) {
	var me struct {
		Username string `json:"username"`
	}
	if err := t.call(ctx, "getMe", map[string]any{}, &me); err != nil {
		return "", err
	}
	return me.Username, nil
}

// FirstChat waits for the first message sent to the bot and returns its chat id (for setup).
func (t *Telegram) FirstChat(ctx context.Context) (int64, error) {
	var offset int64
	for ctx.Err() == nil {
		var ups []tgUpdate
		if err := t.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 25, "allowed_updates": []string{"message"}}, &ups); err != nil {
			if ctx.Err() != nil {
				break
			}
			return 0, err
		}
		for _, u := range ups {
			offset = max(offset, u.UpdateID+1)
			if u.Message != nil {
				// Confirm the update so serve's poller doesn't see it again.
				t.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 0}, nil)
				return u.Message.Chat.ID, nil
			}
		}
	}
	return 0, errors.New("no message arrived in time")
}

// Send posts a plain message (for setup and `notify test`).
func (t *Telegram) Send(ctx context.Context, text string) error {
	return t.call(ctx, "sendMessage", map[string]any{"chat_id": t.ChatID, "text": text}, nil)
}
