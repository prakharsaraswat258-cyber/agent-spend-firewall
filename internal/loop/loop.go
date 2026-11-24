// Package loop spots agents stuck repeating themselves. It is stateless: every
// request carries the full history, so the streak is read from the request body.
package loop

import (
	"crypto/sha256"
	"encoding/json"
	"sort"
	"strings"
)

const (
	Warn = 3 // suspected loop: record an alert
	Stop = 5 // strong loop: block the call
)

type Result struct {
	Count int    // length of the trailing identical streak
	Kind  string // "tool-call" or "error"; "" when none
	Tool  string // tool name only: no inputs are kept
}

type block struct {
	Type     string          `json:"type"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
	IsError  bool            `json:"is_error"`
	Content  json.RawMessage `json:"content"`
	Text     string          `json:"text"`
	Function *struct {
		Name string `json:"name"`
		Args string `json:"arguments"`
	} `json:"function"`
}

type message struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	ToolCalls []block         `json:"tool_calls"`
}

func canon(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	b, _ := json.Marshal(v) // maps marshal with sorted keys
	return string(b)
}

func hash(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return string(h[:8])
}

// Detect reads Anthropic and OpenAI chat message histories.
func Detect(body []byte) Result {
	var req struct {
		Messages []message `json:"messages"`
	}
	if json.Unmarshal(body, &req) != nil {
		return Result{}
	}
	var calls, errs []string // per-turn fingerprints, in order
	var names []string
	for _, m := range req.Messages {
		if m.Role == "user" && humanTurn(m.Content) {
			// A person spoke after the streak (e.g. on resume after a loop stop):
			// start counting again, or a stopped run could never continue.
			calls, errs, names = nil, nil, nil
			continue
		}
		var bl []block
		_ = json.Unmarshal(m.Content, &bl) // string content: no blocks
		bl = append(bl, m.ToolCalls...)
		var c, e, n []string
		for _, b := range bl {
			switch {
			case b.Type == "tool_use":
				c, n = append(c, hash(b.Name, canon(b.Input))), append(n, b.Name)
			case b.Function != nil:
				c, n = append(c, hash(b.Function.Name, canon(json.RawMessage(b.Function.Args)))), append(n, b.Function.Name)
			case b.Type == "tool_result" && b.IsError:
				e = append(e, hash(canon(b.Content)))
			}
		}
		sort.Strings(c)
		sort.Strings(e)
		if m.Role == "assistant" {
			calls, names = append(calls, strings.Join(c, ",")), append(names, strings.Join(n, ","))
		} else if m.Role == "user" || m.Role == "tool" {
			if m.Role == "tool" && len(e) == 0 {
				continue // OpenAI tool messages carry no error flag: tool-call streak only
			}
			errs = append(errs, strings.Join(e, ","))
		}
	}
	r := Result{}
	if n := streak(calls); n > r.Count {
		r = Result{Count: n, Kind: "tool-call", Tool: names[len(names)-1]}
	}
	if n := streak(errs); n > r.Count {
		r = Result{Count: n, Kind: "error", Tool: lastOr(names)}
	}
	return r
}

// humanTurn reports whether a user message carries text typed by a person, not only
// tool results or the agent's injected <system-reminder> notes.
func humanTurn(raw json.RawMessage) bool {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return typed(str)
	}
	var bl []block
	if json.Unmarshal(raw, &bl) != nil {
		return false
	}
	for _, b := range bl {
		if b.Type == "text" && typed(b.Text) {
			return true
		}
	}
	return false
}

func typed(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && !strings.HasPrefix(s, "<system-reminder>")
}

func lastOr(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[len(s)-1]
}

// streak counts trailing equal non-empty fingerprints.
func streak(f []string) int {
	if len(f) == 0 || f[len(f)-1] == "" {
		return 0
	}
	n := 1
	for i := len(f) - 2; i >= 0 && f[i] == f[len(f)-1]; i-- {
		n++
	}
	if n < 2 {
		return 0
	}
	return n
}
