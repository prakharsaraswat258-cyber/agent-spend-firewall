package loop

import (
	"fmt"
	"strings"
	"testing"
)

func anth(n int, input string, errText string) []byte {
	var ms []string
	for i := 0; i < n; i++ {
		ms = append(ms, fmt.Sprintf(`{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":%s}]}`, input))
		ms = append(ms, fmt.Sprintf(`{"role":"user","content":[{"type":"tool_result","is_error":%v,"content":%q}]}`, errText != "", errText))
	}
	return []byte(`{"messages":[` + strings.Join(ms, ",") + `]}`)
}

func TestRepeatedToolCall(t *testing.T) {
	r := Detect(anth(5, `{"b":1,"a":2}`, ""))
	if r.Count != 5 || r.Kind != "tool-call" || r.Tool != "Bash" {
		t.Fatalf("%+v", r)
	}
}

func TestSameErrorDifferentCalls(t *testing.T) {
	var ms []string
	for i := 0; i < 4; i++ {
		ms = append(ms,
			fmt.Sprintf(`{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"n":%d}}]}`, i),
			`{"role":"user","content":[{"type":"tool_result","is_error":true,"content":"patch failed"}]}`)
	}
	r := Detect([]byte(`{"messages":[` + strings.Join(ms, ",") + `]}`))
	if r.Count != 4 || r.Kind != "error" {
		t.Fatalf("%+v", r)
	}
}

func TestProgressBreaksStreak(t *testing.T) {
	b := string(anth(4, `{"x":1}`, ""))
	b = strings.TrimSuffix(b, `]}`) + `,{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"x":2}}]}]}`
	if r := Detect([]byte(b)); r.Count != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestOpenAIToolCalls(t *testing.T) {
	var ms []string
	for i := 0; i < 3; i++ {
		ms = append(ms, `{"role":"assistant","tool_calls":[{"type":"function","function":{"name":"ls","arguments":"{\"p\":\"/\"}"}}]}`,
			`{"role":"tool","content":"ok"}`)
	}
	if r := Detect([]byte(`{"messages":[` + strings.Join(ms, ",") + `]}`)); r.Count != 3 {
		t.Fatalf("%+v", r)
	}
}

func TestNoLoopAndGarbage(t *testing.T) {
	if Detect([]byte(`nope`)).Count != 0 || Detect(anth(1, `{}`, "")).Count != 0 {
		t.Fatal("false positive")
	}
}

func TestPersonSpeakingResetsStreak(t *testing.T) {
	b := strings.TrimSuffix(string(anth(5, `{"x":1}`, "")), `]}`)
	resumed := b + `,{"role":"user","content":"tests need the env var, try another way"}]}`
	if r := Detect([]byte(resumed)); r.Count != 0 {
		t.Fatalf("string message: %+v", r)
	}
	// Claude Code may merge the typed text into the tool-result message.
	merged := strings.TrimSuffix(string(anth(4, `{"x":1}`, "")), `]}`) +
		`,{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"x":1}}]}` +
		`,{"role":"user","content":[{"type":"tool_result","content":"x"},{"type":"text","text":"stop, use make instead"}]}]}`
	if r := Detect([]byte(merged)); r.Count != 0 {
		t.Fatalf("merged message: %+v", r)
	}
	// Injected reminders are not a person: the loop still counts.
	reminder := b + `,{"role":"user","content":[{"type":"text","text":"<system-reminder>todo list empty</system-reminder>"}]}]}`
	if r := Detect([]byte(reminder)); r.Count != 5 {
		t.Fatalf("reminder must not reset: %+v", r)
	}
}
