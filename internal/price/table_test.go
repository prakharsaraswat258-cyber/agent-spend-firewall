package price

import (
	"strings"
	"testing"
	"time"
)

func TestValidateRejectsBadTables(t *testing.T) {
	for name, tb := range map[string]*Table{
		"empty":       {Version: "2026-11-24"},
		"no date":     {Version: "latest", Models: map[string]Entry{"m": {Input: R("1"), Output: R("1")}}},
		"free input":  {Version: "2026-11-24", Models: map[string]Entry{"m": {Input: R("0"), Output: R("1")}}},
		"long no cap": {Version: "2026-11-24", Models: map[string]Entry{"m": {Input: R("1"), Output: R("1"), Long: &Entry{Input: R("2"), Output: R("2")}}}},
	} {
		if tb.Validate() == nil {
			t.Errorf("%s: must be rejected", name)
		}
	}
	if err := Default().Validate(); err != nil {
		t.Fatalf("built-in table: %v", err)
	}
}

func TestStaleAndNewer(t *testing.T) {
	tb := &Table{Version: "2026-11-24b"}
	if tb.Stale(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) || !tb.Stale(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("stale after 30 days")
	}
	if !Newer("2026-11-24b", "2026-11-24") || Newer("2026-10-01", "2026-11-24") {
		t.Fatal("version order")
	}
}

func TestDiffAndCustom(t *testing.T) {
	old := &Table{Version: "2026-01-01", Models: map[string]Entry{"a": {Input: R("10"), Output: R("50")}, "b": {Input: R("1"), Output: R("2")}}}
	nw := &Table{Version: "2026-02-01", Models: map[string]Entry{"a": {Input: R("2"), Output: R("10")}, "c": {Input: R("1"), Output: R("1")}}}
	cs := Diff(old, nw)
	if len(cs) != 3 || cs[0].Model != "a" || cs[0].BigDropPct != 80 || cs[1].New != nil || cs[2].Old != nil {
		t.Fatalf("%+v", cs)
	}
	if s := Summary(cs); s != "1 added, 1 changed, 1 removed" {
		t.Fatal(s)
	}
	if !strings.Contains(cs[0].String(), "[drop 80%]") {
		t.Fatal(cs[0].String())
	}
	m := old.WithCustom(map[string]Entry{"b": {Input: R("9"), Output: R("9")}, "z": {Input: R("1"), Output: R("1")}})
	if m.Models["b"].Input != R("9") || m.Models["z"].Input != R("1") || old.Models["b"].Input != R("1") || !strings.HasSuffix(m.Version, "+custom") {
		t.Fatal("custom must win without changing the base table")
	}
}

func TestUnknownModelNamesTheFix(t *testing.T) {
	_, err := Default().Lookup("mystery-9")
	if err == nil || !strings.Contains(err.Error(), "pitstop prices add mystery-9") || !strings.Contains(err.Error(), "pitstop prices update") {
		t.Fatal(err)
	}
}
