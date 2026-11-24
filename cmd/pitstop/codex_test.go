package main

import (
	"strings"
	"testing"
)

func TestCodexArgs(t *testing.T) {
	a := strings.Join(codexArgs("http://127.0.0.1:4546/run/ab12", `C:\Program Files\pitstop\pitstop.exe`), " | ")
	for _, want := range []string{
		`-c | model_provider="pitstop"`,
		`-c | model_providers.pitstop.base_url="http://127.0.0.1:4546/run/ab12/v1"`,
		`-c | model_providers.pitstop.env_key="OPENAI_API_KEY"`,
		`-c | model_providers.pitstop.wire_api="responses"`,
		`-c | model_providers.pitstop.stream_idle_timeout_ms=660000`,
		`-c | features.hooks=true`,
		`hooks.PreToolUse=[{matcher = "*", hooks = [{type = "command", command = '"C:/Program Files/pitstop/pitstop.exe" hook pre-tool', timeout = 600}]}]`,
	} {
		if !strings.Contains(a, want) {
			t.Errorf("missing %s\nin %s", want, a)
		}
	}
}

func TestUpstreamURL(t *testing.T) {
	if u, err := upstream("https://gw.example.com/anthropic/"); err != nil || u.Path != "/anthropic" {
		t.Fatal(u, err)
	}
	if _, err := upstream("http://localhost:9000"); err != nil {
		t.Fatal("local http gateway must be allowed")
	}
	for _, bad := range []string{"http://gw.example.com", "gw.example.com", "ftp://x"} {
		if _, err := upstream(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
