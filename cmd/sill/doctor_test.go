package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LucasAntunesdeAlmeida/sill/internal/cost"
	"github.com/LucasAntunesdeAlmeida/sill/internal/ledger"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorNotInstalled(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("SILL_CACHE_DIR", t.TempDir())
	var out bytes.Buffer
	err := run([]string{"doctor"}, nil, &out)
	if err == nil {
		t.Error("doctor should fail without a statusLine")
	}
	for _, want := range []string{"FAIL  ", "has no statusLine; run `sill install`", "ok    no sill.json",
		"warn  no SessionEnd hook records session costs", "ok    no session transcripts yet",
		"ok    no session costs recorded yet", "ok    no failed render recorded"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestDoctorFindings(t *testing.T) {
	dir, cache := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("SILL_CACHE_DIR", cache)

	// Installed, but pointing at another sill binary than the one running.
	other := filepath.Join(t.TempDir(), "sill-linux-amd64")
	writeFile(t, other, "")
	writeFile(t, filepath.Join(dir, "settings.json"), `{"statusLine": {"type": "command", "command": "\"`+filepath.ToSlash(other)+`\""},
		"hooks": {"SessionEnd": [{"hooks": [{"type": "command", "command": "\"`+filepath.ToSlash(other)+`\" hook"}]}]}}`)
	// A recorded session that used a model sill has no price for.
	used := cost.Totals{}
	used.Add("claude-opus-5-5", false, time.Time{}, cost.Tokens{Output: 1_000_000})
	used.Add("claude-future-9", false, time.Time{}, cost.Tokens{Output: 1500})
	if err := ledger.Append(ledgerDir(), ledger.Entry{Session: "s", Repo: "/r", Written: time.Now(), Models: used}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "sill.json"), `{"colour": false, "width": 100}`)
	session, err := os.ReadFile(filepath.Join("..", "..", "internal", "transcript", "testdata", "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "projects", "p", "s.jsonl"), string(session))
	writeFile(t, filepath.Join(cache, lastErrorFile), "time: 2026-09-28T10:00:00Z\nversion: dev\nerror: payload: unexpected EOF\n")

	var out bytes.Buffer
	if err := run([]string{"doctor"}, nil, &out); err != nil {
		t.Errorf("warnings alone should not fail: %v\n%s", err, out.String())
	}
	for _, want := range []string{
		"warn  settings.json runs " + filepath.ToSlash(other) + ", not this binary",
		"warn  the SessionEnd hook runs " + filepath.ToSlash(other) + ", not this binary",
		"ok    cost ledger: 1 session(s), $20.00 at list prices, 1 file(s)",
		"warn  no price for claude-future-9 (1.5k tokens)",
		`warn  sill.json: unknown option "colour", ignored`,
		"ok    width fixed at 100 columns",
		"ok    latest transcript: 14 records, 2 agent(s) running, 2 compaction(s)",
		"full scan ",
		"warn  a render failed at 2026-09-28T10:00:00Z: payload: unexpected EOF",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}

	// A statusLine pointing at a binary that is gone cannot work.
	writeFile(t, filepath.Join(dir, "settings.json"), `{"statusLine": {"type": "command", "command": "\"/no/such/dir/sill\""}}`)
	out.Reset()
	if err := run([]string{"doctor"}, nil, &out); err == nil || !strings.Contains(out.String(), "which does not exist") {
		t.Errorf("missing binary: err=%v\n%s", err, out.String())
	}
	for _, r := range out.String() {
		if r > 127 {
			t.Fatalf("non-ASCII output %q", r)
		}
	}
}
