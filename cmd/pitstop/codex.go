package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// codexArgs points Codex at the proxy for one run, with no edits to
// ~/.codex/config.toml: `-c` overrides (parsed as TOML) define a "pitstop"
// provider on the run's base URL, select it, and add the PreToolUse pause hook.
// Codex then uses an API key from OPENAI_API_KEY, which passes through the proxy.
func codexArgs(base, exe string) []string {
	hook := fmt.Sprintf(`[{matcher = "*", hooks = [{type = "command", command = '%s', timeout = 600}]}]`,
		fmt.Sprintf("%q hook pre-tool", filepath.ToSlash(exe)))
	set := func(k, v string) []string { return []string{"-c", k + "=" + v} }
	var a []string
	for _, kv := range [][2]string{
		{"model_providers.pitstop.name", `"Pitstop"`},
		{"model_providers.pitstop.base_url", fmt.Sprintf("%q", base+"/v1")},
		{"model_providers.pitstop.env_key", `"OPENAI_API_KEY"`},
		{"model_providers.pitstop.wire_api", `"responses"`},
		// A pause holds the request up to 9 minutes; Codex's default idle limit is 5.
		{"model_providers.pitstop.stream_idle_timeout_ms", "660000"},
		{"model_provider", `"pitstop"`},
		{"features.hooks", "true"},
		{"hooks.PreToolUse", hook},
	} {
		a = append(a, set(kv[0], kv[1])...)
	}
	return a
}

// codexNotes prints what a Codex user needs to know before the run starts.
func codexNotes() {
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		fmt.Fprintln(os.Stderr, "pitstop: OPENAI_API_KEY is not set. Pitstop meters Codex calls made with an API key; a ChatGPT sign-in is a flat plan and is not metered.")
	}
	fmt.Fprintln(os.Stderr, "pitstop: Codex asks you once to review and trust the Pitstop pause hook (/hooks). Until then, pauses hold the request instead.")
}
