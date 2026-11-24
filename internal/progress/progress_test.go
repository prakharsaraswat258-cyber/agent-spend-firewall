package progress

import (
	"strings"
	"testing"
)

func TestTodoWriteLatestWins(t *testing.T) {
	b := []byte(`{"messages":[
{"role":"assistant","content":[{"type":"tool_use","name":"TodoWrite","input":{"todos":[{"status":"in_progress"},{"status":"pending"}]}}]},
{"role":"user","content":"x"},
{"role":"assistant","content":[{"type":"tool_use","name":"TodoWrite","input":{"todos":[{"status":"completed"},{"status":"completed"},{"status":"pending"}]}}]}]}`)
	if r := Parse(b); !counts(r, 2, 3) {
		t.Fatalf("%+v", r)
	}
}

func TestReportProgressAndNone(t *testing.T) {
	b := []byte(`{"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"report_progress","input":{"done":3,"total":8}}]}]}`)
	if r := Parse(b); r.Done != 3 || r.Total != 8 {
		t.Fatalf("%+v", r)
	}
	if Parse([]byte(`{"messages":[{"role":"user","content":"hi"}]}`)).Found || Parse([]byte(`x`)).Found {
		t.Fatal("false plan")
	}
}

func TestTaskTools(t *testing.T) {
	b := []byte(`{"messages":[
{"role":"assistant","content":[{"type":"tool_use","name":"TaskCreate","input":{"subject":"a"}},{"type":"tool_use","name":"TaskCreate","input":{"subject":"b"}},{"type":"tool_use","name":"TaskCreate","input":{"subject":"c"}}]},
{"role":"user","content":[{"type":"tool_result","content":"Task #1 created"}]},
{"role":"assistant","content":[{"type":"tool_use","name":"TaskUpdate","input":{"taskId":"1","status":"in_progress"}}]},
{"role":"assistant","content":[{"type":"tool_use","name":"TaskUpdate","input":{"taskId":"1","status":"completed"}}]},
{"role":"assistant","content":[{"type":"tool_use","name":"TaskUpdate","input":{"task_id":2,"status":"completed"}}]},
{"role":"assistant","content":[{"type":"tool_use","name":"TaskUpdate","input":{"id":"3","status":"deleted"}}]}]}`)
	if r := Parse(b); !counts(r, 2, 2) {
		t.Fatalf("%+v", r)
	}
}

func TestTaskUpdatesAfterCompaction(t *testing.T) {
	// The TaskCreate calls scrolled out; the updates still show 1 of 2 known tasks done.
	b := []byte(`{"messages":[
{"role":"assistant","content":[{"type":"tool_use","name":"TaskUpdate","input":{"taskId":"4","status":"completed"}},{"type":"tool_use","name":"TaskUpdate","input":{"taskId":"5","status":"in_progress"}}]}]}`)
	if r := Parse(b); !counts(r, 1, 2) {
		t.Fatalf("%+v", r)
	}
}

func TestNewestPlanSourceWins(t *testing.T) {
	b := []byte(`{"messages":[
{"role":"assistant","content":[{"type":"tool_use","name":"TaskCreate","input":{"subject":"a"}}]},
{"role":"assistant","content":[{"type":"tool_use","name":"report_progress","input":{"done":3,"total":8}}]}]}`)
	if r := Parse(b); r.Done != 3 || r.Total != 8 {
		t.Fatalf("%+v", r)
	}
}

func counts(r Result, done, total int) bool { return r.Found && r.Done == done && r.Total == total }

func TestStepNamesAndCurrentStep(t *testing.T) {
	// Claude Code Task tools: ids come from the tool results ("Task #N created").
	b := []byte(`{"messages":[
{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"TaskCreate","input":{"subject":"Write the parser"}},{"type":"tool_use","id":"tu2","name":"TaskCreate","input":{"subject":"Add tests"}}]},
{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"Task #7 created successfully: Write the parser"},{"type":"tool_result","tool_use_id":"tu2","content":[{"type":"text","text":"Task #8 created successfully"}]}]},
{"role":"assistant","content":[{"type":"tool_use","id":"tu3","name":"TaskUpdate","input":{"taskId":"7","status":"completed"}},{"type":"tool_use","id":"tu4","name":"TaskUpdate","input":{"taskId":"8","status":"in_progress"}}]}]}`)
	r := Parse(b)
	if !counts(r, 1, 2) || r.Steps[0].Text != "Write the parser" || r.Steps[1].Status != "in_progress" || r.Current() != "Add tests" {
		t.Fatalf("%+v", r)
	}
	// TodoWrite keeps names; long names are cut.
	long := strings.Repeat("x", 300)
	b = []byte(`{"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"TodoWrite","input":{"todos":[{"content":"` + long + `","status":"in_progress"},{"content":"b","status":"pending"}]}}]}]}`)
	r = Parse(b)
	if len([]rune(r.Current())) != MaxStepText || r.Steps[1].Text != "b" {
		t.Fatalf("%+v", r)
	}
	// report_progress with named steps.
	b = []byte(`{"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"report_progress","input":{"steps":[{"text":"a","status":"done"},{"text":"b","status":"doing"}]}}]}]}`)
	if r = Parse(b); !counts(r, 1, 2) || r.Current() != "b" {
		t.Fatalf("%+v", r)
	}
}

func TestLastToolsNamesOnly(t *testing.T) {
	b := []byte(`{"messages":[
{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"/secret/.env"}}]},
{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{}},{"type":"tool_use","name":"Bash","input":{"command":"export KEY=sk-123"}}]},
{"role":"assistant","tool_calls":[{"function":{"name":"run_tests","arguments":"{}"}}]}]}`)
	got := LastTools(b, 3)
	if strings.Join(got, ",") != "Edit,Bash,run_tests" {
		t.Fatalf("%v", got)
	}
}
