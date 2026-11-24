// Package progress reads the agent's own plan from the request history:
// Claude Code's Task tools (TaskCreate/TaskUpdate) or older TodoWrite list,
// or a report_progress tool call.
package progress

import "encoding/json"

type Result struct {
	Done, Total int
	Found       bool
}

type block struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Input struct {
		Todos []struct {
			Status string `json:"status"`
		} `json:"todos"`
		Done  *int `json:"done"`
		Total *int `json:"total"`
		// TaskUpdate. The model may emit id or task_id; Claude Code repairs both to taskId.
		TaskID  json.RawMessage `json:"taskId"`
		ID      json.RawMessage `json:"id"`
		TaskID2 json.RawMessage `json:"task_id"`
		Status  string          `json:"status"`
	} `json:"input"`
}

// tasks follows Claude Code's Task tools, which change one item per call.
type tasks struct {
	created int
	status  map[string]string // task id -> latest status from TaskUpdate
}

func (t *tasks) result() Result {
	done, gone, known := 0, 0, 0
	for _, s := range t.status {
		switch s {
		case "completed":
			done++
			known++
		case "deleted":
			gone++
		default:
			known++
		}
	}
	// Creates can scroll out of a compacted history; updates still prove the task exists.
	total := max(t.created-gone, known)
	if total <= 0 {
		return Result{}
	}
	return Result{Done: min(done, total), Total: total, Found: true}
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
	tk := tasks{status: map[string]string{}}
	useTasks := false // true when the newest plan signal came from the Task tools
	for _, m := range req.Messages {
		if m.Role != "assistant" {
			continue
		}
		var bl []block
		if json.Unmarshal(m.Content, &bl) != nil {
			continue
		}
		for _, b := range bl {
			if b.Type != "tool_use" {
				continue
			}
			switch b.Name {
			case "TodoWrite":
				r := Result{Total: len(b.Input.Todos), Found: len(b.Input.Todos) > 0}
				for _, t := range b.Input.Todos {
					if t.Status == "completed" {
						r.Done++
					}
				}
				if r.Found {
					res, useTasks = r, false
				}
			case "report_progress":
				if b.Input.Done != nil && b.Input.Total != nil && *b.Input.Total > 0 {
					res, useTasks = Result{Done: min(*b.Input.Done, *b.Input.Total), Total: *b.Input.Total, Found: true}, false
				}
			case "TaskCreate":
				tk.created++
				useTasks = true
			case "TaskUpdate":
				if id := taskID(b.Input.TaskID, b.Input.ID, b.Input.TaskID2); id != "" && b.Input.Status != "" {
					tk.status[id] = b.Input.Status
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
