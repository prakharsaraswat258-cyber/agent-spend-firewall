// Package progress reads the agent's own plan from the request history:
// Claude Code's Task tools (TaskCreate/TaskUpdate) or older TodoWrite list,
// or a report_progress tool call.
package progress

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MaxStepText is the longest step name kept. Step names are allowed metadata;
// prompts and code are not, so names are cut short.
const MaxStepText = 120

type Step struct {
	Text   string
	Status string // "pending", "in_progress" or "completed"
}

type Result struct {
	Done, Total int
	Found       bool
	Steps       []Step // in plan order; empty when the source gives counts only
}

// Current is the step being worked on (the first in_progress one), or "".
func (r Result) Current() string {
	for _, s := range r.Steps {
		if s.Status == "in_progress" {
			return s.Text
		}
	}
	return ""
}

// Equal compares counts and steps (Found ignored).
func (r Result) Equal(o Result) bool {
	if r.Done != o.Done || r.Total != o.Total || len(r.Steps) != len(o.Steps) {
		return false
	}
	for i := range r.Steps {
		if r.Steps[i] != o.Steps[i] {
			return false
		}
	}
	return true
}

type block struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	Input     struct {
		Todos []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
		Steps []struct {
			Text   string `json:"text"`
			Status string `json:"status"`
		} `json:"steps"`
		Done  *int `json:"done"`
		Total *int `json:"total"`
		// TaskCreate / TaskUpdate. The model may emit id or task_id; Claude Code repairs both to taskId.
		Subject string          `json:"subject"`
		TaskID  json.RawMessage `json:"taskId"`
		ID      json.RawMessage `json:"id"`
		TaskID2 json.RawMessage `json:"task_id"`
		Status  string          `json:"status"`
	} `json:"input"`
}

// tasks follows Claude Code's Task tools, which change one item per call.
type tasks struct {
	order   []string          // task ids in creation order
	subject map[string]string // id -> subject
	status  map[string]string // id -> latest status
	pending map[string]string // tool_use id of a TaskCreate -> subject, until its result names the id
	created int
}

var taskNum = regexp.MustCompile(`#(\d+)`)

func (t *tasks) add(id, subject string) {
	if _, ok := t.subject[id]; !ok {
		t.order = append(t.order, id)
	}
	t.subject[id] = subject
	if t.status[id] == "" {
		t.status[id] = "pending"
	}
}

func (t *tasks) result() Result {
	// Updates for tasks whose TaskCreate scrolled out of a compacted history still count.
	for id := range t.status {
		if _, ok := t.subject[id]; !ok {
			t.order = append(t.order, id)
			t.subject[id] = "task " + id
		}
	}
	sort.SliceStable(t.order, func(i, j int) bool { return numLess(t.order[i], t.order[j]) })
	var r Result
	for _, id := range t.order {
		st := norm(t.status[id])
		if st == "deleted" {
			continue
		}
		r.Steps = append(r.Steps, Step{Text: cut(t.subject[id]), Status: st})
	}
	// Creates whose id never showed up (result not in this request) still count as planned.
	for _, s := range t.pending {
		r.Steps = append(r.Steps, Step{Text: cut(s), Status: "pending"})
	}
	return finish(r)
}

func numLess(a, b string) bool {
	x, e1 := strconv.Atoi(a)
	y, e2 := strconv.Atoi(b)
	if e1 == nil && e2 == nil {
		return x < y
	}
	return a < b
}

func taskID(raws ...json.RawMessage) string {
	for _, r := range raws {
		if len(r) == 0 || string(r) == "null" {
			continue
		}
		var s string
		if json.Unmarshal(r, &s) == nil {
			return s
		}
		return string(r) // numeric id
	}
	return ""
}

func norm(s string) string {
	switch s {
	case "completed", "done":
		return "completed"
	case "in_progress", "doing", "active":
		return "in_progress"
	case "deleted":
		return "deleted"
	}
	return "pending"
}

func cut(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > MaxStepText {
		return string(r[:MaxStepText-1]) + "…"
	}
	return s
}

func finish(r Result) Result {
	r.Total, r.Done = len(r.Steps), 0
	for _, s := range r.Steps {
		if s.Status == "completed" {
			r.Done++
		}
	}
	r.Found = r.Total > 0
	return r
}

func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var bl []struct {
		Text string `json:"text"`
	}
	json.Unmarshal(raw, &bl)
	var b strings.Builder
	for _, x := range bl {
		b.WriteString(x.Text)
	}
	return b.String()
}

// Parse returns the latest plan state found in an Anthropic messages body.
func Parse(body []byte) Result {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &req) != nil {
		return Result{}
	}
	res := Result{}
	tk := tasks{subject: map[string]string{}, status: map[string]string{}, pending: map[string]string{}}
	useTasks := false // true when the newest plan signal came from the Task tools
	for _, m := range req.Messages {
		var bl []block
		if json.Unmarshal(m.Content, &bl) != nil {
			continue
		}
		for _, b := range bl {
			// "Task #3 created successfully": ties a TaskCreate to the id later updates use.
			if m.Role == "user" && b.Type == "tool_result" {
				if subj, ok := tk.pending[b.ToolUseID]; ok {
					delete(tk.pending, b.ToolUseID)
					if mm := taskNum.FindStringSubmatch(resultText(b.Content)); mm != nil {
						tk.add(mm[1], subj)
					} else {
						tk.add(strconv.Itoa(tk.created), subj)
					}
				}
				continue
			}
			if m.Role != "assistant" || b.Type != "tool_use" {
				continue
			}
			switch b.Name {
			case "TodoWrite":
				var r Result
				for _, t := range b.Input.Todos {
					r.Steps = append(r.Steps, Step{Text: cut(t.Content), Status: norm(t.Status)})
				}
				if r = finish(r); r.Found {
					res, useTasks = r, false
				}
			case "report_progress":
				if len(b.Input.Steps) > 0 {
					var r Result
					for _, s := range b.Input.Steps {
						r.Steps = append(r.Steps, Step{Text: cut(s.Text), Status: norm(s.Status)})
					}
					res, useTasks = finish(r), false
				} else if b.Input.Done != nil && b.Input.Total != nil && *b.Input.Total > 0 {
					res, useTasks = Result{Done: min(*b.Input.Done, *b.Input.Total), Total: *b.Input.Total, Found: true}, false
				}
			case "TaskCreate":
				tk.created++
				if b.ID == "" {
					tk.add(strconv.Itoa(tk.created), b.Input.Subject) // no id to match a result: number in order
				} else {
					tk.pending[b.ID] = b.Input.Subject
				}
				useTasks = true
			case "TaskUpdate":
				if id := taskID(b.Input.TaskID, b.Input.ID, b.Input.TaskID2); id != "" && b.Input.Status != "" {
					tk.status[id] = norm(b.Input.Status)
					if b.Input.Status == "deleted" {
						tk.status[id] = "deleted"
					}
					useTasks = true
				}
			}
		}
	}
	if useTasks {
		if r := tk.result(); r.Found {
			return r
		}
	}
	return res
}

// LastTools returns the names of the agent's last n tool calls, newest last.
// Names only: tool inputs can hold secrets and are never kept. Reads Anthropic
// tool_use blocks and OpenAI tool_calls.
func LastTools(body []byte, n int) []string {
	var req struct {
		Messages []struct {
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &req) != nil {
		return nil
	}
	var names []string
	for _, m := range req.Messages {
		if m.Role != "assistant" {
			continue
		}
		var bl []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		json.Unmarshal(m.Content, &bl)
		for _, b := range bl {
			if b.Type == "tool_use" && b.Name != "" {
				names = append(names, cut(b.Name))
			}
		}
		for _, tc := range m.ToolCalls {
			if tc.Function.Name != "" {
				names = append(names, cut(tc.Function.Name))
			}
		}
	}
	if len(names) > n {
		names = names[len(names)-n:]
	}
	return names
}
