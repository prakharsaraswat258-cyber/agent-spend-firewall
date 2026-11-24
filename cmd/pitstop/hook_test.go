package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func runHook(t *testing.T, addr, stdin string) (int, string) {
	t.Helper()
	in, _ := os.CreateTemp(t.TempDir(), "in")
	in.WriteString(stdin)
	in.Seek(0, 0)
	errf, _ := os.CreateTemp(t.TempDir(), "err")
	oldIn, oldErr := os.Stdin, os.Stderr
	os.Stdin, os.Stderr = in, errf
	code := hookCmd(Config{Addr: addr}, []string{"pre-tool"})
	os.Stdin, os.Stderr = oldIn, oldErr
	in.Close()
	errf.Close()
	b, _ := os.ReadFile(errf.Name())
	return code, string(b)
}

func TestHookBlocksWithExit2AndReason(t *testing.T) {
	var gotRun string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pitstop/gate" || r.Header.Get("X-Pitstop") != "1" {
			t.Errorf("bad call %s", r.URL.Path)
		}
		b := make([]byte, 100)
		n, _ := r.Body.Read(b)
		gotRun = string(b[:n])
		w.Write([]byte(`{"allow":false,"message":"Pitstop blocked this call: you chose Stop."}`))
	}))
	defer srv.Close()
	t.Setenv("PITSTOP_RUN", "")
	code, errText := runHook(t, strings.TrimPrefix(srv.URL, "http://"), `{"session_id":"abcdef12-3456-7890-abcd-ef1234567890","tool_name":"Bash"}`)
	if code != 2 || !strings.Contains(errText, "you chose Stop") {
		t.Fatalf("code %d stderr %q", code, errText)
	}
	if !strings.Contains(gotRun, `"cc-abcdef12"`) {
		t.Fatalf("session run not used: %s", gotRun)
	}
}

func TestHookAllowsAndToleratesDownProxy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"allow":true}`))
	}))
	t.Setenv("PITSTOP_RUN", "r1")
	if code, _ := runHook(t, strings.TrimPrefix(srv.URL, "http://"), `{}`); code != 0 {
		t.Fatalf("allow: %d", code)
	}
	srv.Close()
	if code, _ := runHook(t, "127.0.0.1:1", `{}`); code != 0 {
		t.Fatalf("down proxy must not block tools: %d", code)
	}
}

func TestHookSettingsFile(t *testing.T) {
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HOME", os.Getenv("USERPROFILE"))
	path, err := claudeHookSettings()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"PreToolUse"`) || !strings.Contains(string(b), `hook pre-tool`) || !strings.Contains(string(b), `"timeout": 600`) {
		t.Fatalf("%s", b)
	}
}
