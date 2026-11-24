package progress

import "testing"

func TestTodoWriteLatestWins(t *testing.T) {
	b := []byte(`{"messages":[
{"role":"assistant","content":[{"type":"tool_use","name":"TodoWrite","input":{"todos":[{"status":"in_progress"},{"status":"pending"}]}}]},
{"role":"user","content":"x"},
{"role":"assistant","content":[{"type":"tool_use","name":"TodoWrite","input":{"todos":[{"status":"completed"},{"status":"completed"},{"status":"pending"}]}}]}]}`)
	if r := Parse(b); r != (Result{Done: 2, Total: 3, Found: true}) {
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
	if r := Parse(b); r != (Result{Done: 2, Total: 2, Found: true}) {
		t.Fatalf("%+v", r)
	}
}

func TestTaskUpdatesAfterCompaction(t *testing.T) {
	// The TaskCreate calls scrolled out; the updates still show 1 of 2 known tasks done.
	b := []byte(`{"messages":[
{"role":"assistant","content":[{"type":"tool_use","name":"TaskUpdate","input":{"taskId":"4","status":"completed"}},{"type":"tool_use","name":"TaskUpdate","input":{"taskId":"5","status":"in_progress"}}]}]}`)
	if r := Parse(b); r != (Result{Done: 1, Total: 2, Found: true}) {
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
