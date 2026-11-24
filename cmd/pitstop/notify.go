package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/decide"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/ledger"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/notify"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/proxy"
)

const tokenEnv = "PITSTOP_TELEGRAM_BOT_TOKEN"

// telegramToken reads the bot token from the environment, else from .env in the
// Pitstop folder. It is never written to the ledger or printed.
func telegramToken() string {
	if t := strings.TrimSpace(os.Getenv(tokenEnv)); t != "" {
		return t
	}
	f, err := os.Open(filepath.Join(filepath.Dir(cfgPath()), ".env"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "="); ok && strings.TrimSpace(k) == tokenEnv {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// channelOn: desktop is on unless disabled; telegram is off unless enabled.
func channelOn(l *ledger.Ledger, ch string) bool {
	v, ok := l.Setting("notify_" + ch)
	if !ok {
		return ch == "desktop"
	}
	return v == 1
}

// setupNotify builds the channels `serve` sends cards to.
func setupNotify(ctx context.Context, cfg Config, addr string, l *ledger.Ledger, dec *decide.Decider, p *proxy.Proxy, timeout time.Duration) *notify.Hub {
	hub := notify.NewHub(timeout, func(id string) bool { _, ok := dec.Card(id); return ok })
	if channelOn(l, "desktop") {
		if runtime.GOOS == "windows" {
			hub.Add(notify.Toast{URL: "http://" + addr + "/dashboard"})
		} else {
			hub.Add(notify.Func{N: "desktop", F: func(c decide.Card) { desktopNotify(cfg, c, timeout) }})
		}
	}
	if channelOn(l, "telegram") {
		tok := telegramToken()
		chat, ok := l.Setting("telegram_chat_id")
		switch {
		case tok == "":
			fmt.Fprintf(os.Stderr, "pitstop: Telegram is on but %s is not set (environment or %s). Cards go to the other channels.\n", tokenEnv, filepath.Join(filepath.Dir(cfgPath()), ".env"))
		case !ok:
			fmt.Fprintln(os.Stderr, "pitstop: Telegram is on but not set up. Run: pitstop notify telegram setup")
		default:
			tg := &notify.Telegram{API: notify.TelegramAPI, Token: tok, ChatID: chat,
				Answer: func(id string, cont bool, add int64) error { return p.Respond(id, cont, add, "telegram") }}
			hub.Add(tg)
			go tg.Poll(ctx)
		}
	}
	return hub
}

// notifyCmd: pitstop notify enable|disable <desktop|telegram> | telegram setup | test
func notifyCmd(cfg Config, args []string) int {
	usage := func() int {
		fmt.Fprintln(os.Stderr, "usage: pitstop notify enable|disable <desktop|telegram> | notify telegram setup | notify test")
		return 2
	}
	if len(args) == 0 {
		return usage()
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DB), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	l, err := ledger.Open(cfg.DB)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer l.Close()
	switch {
	case (args[0] == "enable" || args[0] == "disable") && len(args) == 2 && (args[1] == "desktop" || args[1] == "telegram"):
		v := int64(0)
		if args[0] == "enable" {
			v = 1
		}
		if err := l.SetSetting("notify_"+args[1], v); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("%s notifications %sd. Restart pitstop serve to apply.\n", args[1], args[0])
		if args[1] == "telegram" && v == 1 {
			if _, ok := l.Setting("telegram_chat_id"); !ok {
				fmt.Println("Telegram is not set up yet: run pitstop notify telegram setup")
			}
		}
		return 0
	case len(args) == 2 && args[0] == "telegram" && args[1] == "setup":
		return telegramSetup(l)
	case len(args) == 1 && args[0] == "test":
		return notifyTest(l)
	}
	return usage()
}

func telegramSetup(l *ledger.Ledger) int {
	tok := telegramToken()
	if tok == "" {
		fmt.Printf(`Telegram setup (optional; a solo developer never needs it):
1. In Telegram, message @BotFather, send /newbot and follow the steps.
2. Put the token it gives you in the environment as %s,
   or in %s as a line: %s=<token>
   Pitstop never stores the token in its ledger or logs.
3. Run this command again.
`, tokenEnv, filepath.Join(filepath.Dir(cfgPath()), ".env"), tokenEnv)
		return 1
	}
	tg := &notify.Telegram{API: notify.TelegramAPI, Token: tok}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	name, err := tg.BotName(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the token did not work: %v\n", err)
		return 1
	}
	fmt.Printf("Bot @%s found. Now send it any message from your phone (waiting up to 3 minutes; stop pitstop serve first if it is running)...\n", name)
	chat, err := tg.FirstChat(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "no message arrived: %v\n", err)
		return 1
	}
	tg.ChatID = chat
	if err := l.SetSetting("telegram_chat_id", chat); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := l.SetSetting("notify_telegram", 1); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	tg.Send(ctx, "Pitstop is connected. Decision cards will arrive here with Continue and Stop buttons. Only this chat can answer.")
	fmt.Println("Connected. Only this chat can answer cards. Restart pitstop serve to start using it; check with: pitstop notify test")
	return 0
}

// notifyTest sends a sample card to every enabled channel. Its buttons answer nothing.
func notifyTest(l *ledger.Ledger) int {
	c := decide.Card{ID: "test", Run: "test-run", Reason: "forecast", Spent: 6_100_000, Budget: 10_000_000,
		P10: 2_500_000, P50: 5_000_000, P90: 9_000_000, Offer: 9_000_000, Confidence: "medium", Done: 3, Total: 8}
	sent := 0
	if channelOn(l, "desktop") {
		if runtime.GOOS == "windows" {
			if err := (notify.Toast{URL: "http://" + loadConfig().Addr + "/dashboard"}).Card(c); err != nil {
				fmt.Fprintf(os.Stderr, "desktop: %v\n", err)
			} else {
				fmt.Println("desktop: sent")
				sent++
			}
		} else {
			fmt.Println("desktop: macOS/Linux show a dialog when a real card opens (no test dialog)")
		}
	}
	if channelOn(l, "telegram") {
		chat, ok := l.Setting("telegram_chat_id")
		tok := telegramToken()
		if !ok || tok == "" {
			fmt.Fprintln(os.Stderr, "telegram: not set up. Run: pitstop notify telegram setup")
		} else {
			tg := &notify.Telegram{API: notify.TelegramAPI, Token: tok, ChatID: chat}
			if err := tg.Send(context.Background(), "Pitstop test card (buttons are not live):\n\n"+c.Text()); err != nil {
				fmt.Fprintf(os.Stderr, "telegram: %v\n", err)
			} else {
				fmt.Println("telegram: sent")
				sent++
			}
		}
	}
	if sent == 0 {
		fmt.Println("no channel sent a test card")
		return 1
	}
	return 0
}
