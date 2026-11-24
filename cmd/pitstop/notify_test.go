package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/ledger"
)

func TestTelegramTokenFromEnvOrDotEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv(tokenEnv, "")
	if telegramToken() != "" {
		t.Fatal("no token configured")
	}
	os.MkdirAll(filepath.Join(home, ".pitstop"), 0o700)
	os.WriteFile(filepath.Join(home, ".pitstop", ".env"), []byte("# comment\nOTHER=1\n"+tokenEnv+"=\"123:abc\"\n"), 0o600)
	if got := telegramToken(); got != "123:abc" {
		t.Fatalf(".env token %q", got)
	}
	t.Setenv(tokenEnv, "999:env")
	if got := telegramToken(); got != "999:env" {
		t.Fatalf("environment must win: %q", got)
	}
}

func TestChannelDefaults(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if !channelOn(l, "desktop") || channelOn(l, "telegram") {
		t.Fatal("desktop on, telegram off by default")
	}
	l.SetSetting("notify_desktop", 0)
	l.SetSetting("notify_telegram", 1)
	if channelOn(l, "desktop") || !channelOn(l, "telegram") {
		t.Fatal("settings not applied")
	}
}
