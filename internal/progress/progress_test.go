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
