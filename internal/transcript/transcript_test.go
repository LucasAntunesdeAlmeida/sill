package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var sessionStart = time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

func TestScanReader(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	act := ScanReader(strings.NewReader(string(data)))
	if act.Compactions != 2 {
		t.Errorf("compactions = %d, want 2", act.Compactions)
	}
	if act.Agents != 2 { // B and D in testdata/session.jsonl
		t.Errorf("agents = %d, want 2", act.Agents)
	}
	if !act.Start.Equal(sessionStart) {
		t.Errorf("start = %v, want %v", act.Start, sessionStart)
	}
}

func TestScanFile(t *testing.T) {
	if act := Scan(filepath.Join("testdata", "session.jsonl")); act.Compactions != 2 || act.Agents != 2 || !act.Start.Equal(sessionStart) {
		t.Errorf("file scan = %+v", act)
	}
	if act := Scan(""); act != (Activity{}) {
		t.Errorf("no path = %+v", act)
	}
	if act := Scan(filepath.Join(t.TempDir(), "missing.jsonl")); act != (Activity{}) {
		t.Errorf("missing file = %+v", act)
	}
	if act := ScanReader(strings.NewReader("")); act != (Activity{}) {
		t.Errorf("empty = %+v", act)
	}
}

func TestStart(t *testing.T) {
	if got := Start(filepath.Join("testdata", "session.jsonl")); !got.Equal(sessionStart) {
		t.Errorf("Start = %v", got)
	}
	if got := Start(""); !got.IsZero() {
		t.Errorf("no path = %v", got)
	}
	if got := Start(filepath.Join(t.TempDir(), "missing.jsonl")); !got.IsZero() {
		t.Errorf("missing file = %v", got)
	}
	// The first timestamp wins even when earlier records have none, but only within the head.
	head := `{"type":"summary"}` + "\n" + `{"timestamp":"2026-01-02T03:04:05.678Z"}` + "\n" + `{"timestamp":"2026-01-03T00:00:00Z"}` + "\n"
	if got := StartReader(strings.NewReader(head)); !got.Equal(time.Date(2026, 1, 2, 3, 4, 5, 678000000, time.UTC)) {
		t.Errorf("head = %v", got)
	}
	late := strings.Repeat(`{"type":"summary"}`+"\n", headRecords) + `{"timestamp":"2026-01-02T03:04:05Z"}` + "\n"
	if got := StartReader(strings.NewReader(late)); !got.IsZero() {
		t.Errorf("timestamp past the head should be ignored, got %v", got)
	}
	if got := StartReader(strings.NewReader(`{"timestamp":"yesterday"}` + "\n")); !got.IsZero() {
		t.Errorf("bad timestamp = %v", got)
	}
}

func TestOversizedRecordStopsQuietly(t *testing.T) {
	huge := `{"type":"system","subtype":"compact_boundary"}` + "\n" + `{"pad":"` + strings.Repeat("x", maxRecord+1) + `"}` + "\n"
	if act := ScanReader(strings.NewReader(huge)); act.Compactions != 1 {
		t.Errorf("should keep what was read before the oversized record, got %+v", act)
	}
}

func TestBetween(t *testing.T) {
	got := between([]byte("<a>1</a> junk <a>2</a> <a>unterminated"), []byte("<a>"), []byte("</a>"))
	if len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Errorf("between = %q", got)
	}
}
