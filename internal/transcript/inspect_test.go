package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspect(t *testing.T) {
	rep, err := Inspect(filepath.Join("testdata", "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// Line 13 quotes a launch in escaped JSON: neither pattern may count it.
	if rep.Records != 14 || rep.Launches != 4 || rep.LooseLaunches != 4 || rep.LooseCompactions != 2 {
		t.Errorf("report = %+v", rep)
	}
	if drift := rep.Drift(); len(drift) != 0 {
		t.Errorf("the fixture should show no drift: %q", drift)
	}
	if _, err := Inspect(filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Error("missing file should be an error")
	}
}

// A transcript written with spaces after colons, as a future Claude Code might, is
// flagged instead of silently showing no agents.
func TestInspectDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	doc := strings.Join([]string{
		`{"type": "assistant", "message": {"content": [{"type": "tool_use", "id": "toolu_A", "name": "Agent"}]}}`,
		`{"type": "system", "subtype": "compact_boundary"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"1 of 1 agent launches not recognised",
		"1 of 1 compactions not recognised",
		"no record has a timestamp sill can read",
	}
	if got := rep.Drift(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("drift = %q", got)
	}
}
