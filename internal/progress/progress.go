// Package progress reads the agent's own plan from the request history:
// Claude Code's TodoWrite list, or a report_progress tool call.
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
	} `json:"input"`
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
					res = r
				}
			case "report_progress":
				if b.Input.Done != nil && b.Input.Total != nil && *b.Input.Total > 0 {
					res = Result{Done: min(*b.Input.Done, *b.Input.Total), Total: *b.Input.Total, Found: true}
				}
			}
		}
	}
	return res
}
