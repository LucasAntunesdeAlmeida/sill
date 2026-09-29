package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LucasAntunesdeAlmeida/sill/internal/cost"
	"github.com/LucasAntunesdeAlmeida/sill/internal/ledger"
)

// transcriptDoc is a session with one Opus 5.5 response of outTokens output tokens, made
// in cwd.
func transcriptDoc(cwd string, outTokens int) string {
	quoted, _ := json.Marshal(cwd)
	return fmt.Sprintf(`{"type":"user","timestamp":"2026-09-29T10:00:00Z","message":{"content":"hi"}}`+"\n"+
		`{"message":{"model":"claude-opus-5-5","id":"msg_1","usage":{"input_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":%d}},"type":"assistant","timestamp":"2026-09-29T10:00:05Z","cwd":%s}`+"\n",
		outTokens, quoted)
}

// hookEnv points sill at an empty Claude config and cache, and returns the projects folder.
func hookEnv(t *testing.T) string {
	t.Helper()
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("SILL_CACHE_DIR", t.TempDir())
	return filepath.Join(config, "projects")
}

func runHook(t *testing.T, input map[string]string) {
	t.Helper()
	doc, _ := json.Marshal(input)
	var out bytes.Buffer
	if err := run([]string{"hook"}, bytes.NewReader(doc), &out); err != nil || out.Len() != 0 {
		t.Fatalf("hook: err=%v out=%q", err, out.String())
	}
	if data, err := os.ReadFile(filepath.Join(cacheDir(), lastErrorFile)); err == nil {
		t.Fatalf("hook recorded an error:\n%s", data)
	}
}

func readLedger(t *testing.T) (entries []ledger.Entry, lines int) {
	t.Helper()
	entries, bad, err := ledger.Read(ledgerDir())
	if err != nil || bad != 0 {
		t.Fatalf("ledger: %v, %d bad lines", err, bad)
	}
	files, _ := filepath.Glob(filepath.Join(ledgerDir(), "*.jsonl"))
	for _, f := range files {
		data, _ := os.ReadFile(f)
		lines += strings.Count(string(data), "\n")
	}
	return entries, lines
}

func TestHookRecordsAndSweeps(t *testing.T) {
	projects := hookEnv(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "pkg")
	project := filepath.Join(projects, "C--repo")
	ended := filepath.Join(project, "ended.jsonl")
	killed := filepath.Join(project, "killed.jsonl")
	old := filepath.Join(project, "old.jsonl")
	empty := filepath.Join(project, "empty.jsonl")
	writeFile(t, ended, transcriptDoc(sub, 1000))
	writeFile(t, killed, transcriptDoc(repo, 2000))
	writeFile(t, old, transcriptDoc(repo, 3000))
	writeFile(t, empty, `{"type":"user","message":{"content":"hi"}}`+"\n")
	writeFile(t, filepath.Join(project, "agent-a1.jsonl"), transcriptDoc(repo, 4000))
	past := time.Now().Add(-sweepAge - time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	runHook(t, map[string]string{"session_id": "ended", "transcript_path": ended, "cwd": sub, "hook_event_name": "SessionEnd"})
	entries, lines := readLedger(t)
	got := map[string]ledger.Entry{}
	for _, e := range entries {
		got[e.Session] = e
	}
	// The ended session and the one killed without a hook; not the month-old one, the one
	// without responses or an old-style subagent file.
	if len(got) != 2 || lines != 2 {
		t.Fatalf("sessions = %v, %d lines", entries, lines)
	}
	e := got["ended"]
	want, _ := cost.Price("claude-opus-5-5", false, time.Time{}, cost.Tokens{Output: 1000})
	if math.Abs(e.USD-want) > 1e-12 || e.Repo != repo || e.Cwd != sub || e.Prices != cost.TableVersion {
		t.Errorf("ended = %+v, want $%v in %s", e, want, repo)
	}
	if !e.Start.Equal(time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)) || !e.End.Equal(time.Date(2026, 9, 29, 10, 0, 5, 0, time.UTC)) {
		t.Errorf("times = %v - %v", e.Start, e.End)
	}
	if got["killed"].Models.Tokens().Output != 2000 {
		t.Errorf("killed = %+v", got["killed"])
	}

	// Nothing changed: only the session the hook names is written again.
	runHook(t, map[string]string{"session_id": "ended", "transcript_path": ended})
	if _, lines := readLedger(t); lines != 3 {
		t.Errorf("second run wrote %d lines in all, want 3", lines)
	}

	// The killed session was resumed and grew: the next hook, for another session, updates it.
	future := time.Now().Add(time.Minute)
	f, err := os.OpenFile(killed, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(strings.Replace(strings.SplitAfter(transcriptDoc(repo, 500), "\n")[1], "msg_1", "msg_2", 1))
	f.Close()
	if err := os.Chtimes(killed, future, future); err != nil {
		t.Fatal(err)
	}
	runHook(t, map[string]string{})
	entries, _ = readLedger(t)
	for _, e := range entries {
		if e.Session == "killed" && e.Models.Tokens().Output != 2500 {
			t.Errorf("resumed session = %+v", e)
		}
	}
}

func TestFromMSYS(t *testing.T) {
	for in, want := range map[string]string{
		"/c/Users/me/repo": `C:\Users\me\repo`,
		"/d/":              `D:\`,
		"/c":               `C:\`,
		"/C/x":             `C:\x`,
	} {
		if got, ok := fromMSYS(in); !ok || got != want {
			t.Errorf("fromMSYS(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "/", "/home/me", "/cd/x", `C:\Users`, "c/Users", "/1/x"} {
		if got, ok := fromMSYS(in); ok {
			t.Errorf("fromMSYS(%q) = %q, should not convert", in, got)
		}
	}
}

// A session that moved from one repository to another is split between them, and the
// report shows each its share.
func TestSessionSplitBetweenRepositories(t *testing.T) {
	projects := hookEnv(t)
	root := t.TempDir()
	alpha, beta := filepath.Join(root, "alpha"), filepath.Join(root, "beta")
	for _, r := range []string{alpha, beta} {
		if err := os.MkdirAll(filepath.Join(r, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	first := strings.SplitAfter(transcriptDoc(alpha, 100_000), "\n")
	second := strings.SplitAfter(transcriptDoc(filepath.Join(beta, "pkg"), 300_000), "\n")[1]
	path := filepath.Join(projects, "p", "moved.jsonl")
	writeFile(t, path, first[0]+first[1]+strings.Replace(second, "msg_1", "msg_2", 1))

	runHook(t, map[string]string{"transcript_path": path})
	entries, _ := readLedger(t)
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	e := entries[0]
	if e.V != ledger.Version || e.Repo != beta || len(e.Parts) != 2 || e.Parts[0].Repo != beta || e.Parts[1].Repo != alpha {
		t.Fatalf("entry = %+v", e)
	}
	if e.Parts[0].Models.Tokens().Output != 300_000 || e.Parts[1].Models.Tokens().Output != 100_000 || e.Models.Tokens().Output != 400_000 {
		t.Errorf("shares = %+v", e.Parts)
	}

	var out bytes.Buffer
	if err := run([]string{"cost"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	text := collapse(out.String())
	for _, want := range []string{"/beta 1 0 300k $6.00", "/alpha 1 0 100k $2.00", "total 1 0 400k $8.00"} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := run([]string{"cost", alpha}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if text := collapse(out.String()); !strings.Contains(text, "100k $2.00 opus-5-5 moved*") || !strings.Contains(text, "only its share here") {
		t.Errorf("alpha report:\n%s", out.String())
	}
}

// Lines written before sessions were split are recorded again while the transcript lasts.
func TestSweepRewritesOldLines(t *testing.T) {
	projects := hookEnv(t)
	repo := t.TempDir()
	path := filepath.Join(projects, "p", "old.jsonl")
	writeFile(t, path, transcriptDoc(repo, 1000))
	// Recorded after the transcript last changed: only the format asks for a new line.
	changed := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, changed, changed); err != nil {
		t.Fatal(err)
	}
	old := ledger.Entry{Session: "old", Repo: "/somewhere/else", Written: time.Now().Add(-time.Hour), Models: cost.Totals{}}
	if err := ledger.Append(ledgerDir(), old); err != nil {
		t.Fatal(err)
	}
	runHook(t, map[string]string{})
	entries, _ := readLedger(t)
	if len(entries) != 1 || entries[0].V != ledger.Version || entries[0].Repo != repo {
		t.Errorf("entries = %+v", entries)
	}
}

// A hook that cannot write its ledger still exits cleanly and leaves the reason for doctor.
func TestHookFailureGoesToLastError(t *testing.T) {
	projects := hookEnv(t)
	path := filepath.Join(projects, "p", "s.jsonl")
	writeFile(t, path, transcriptDoc(t.TempDir(), 10))
	writeFile(t, ledgerDir(), "a file where the ledger folder should be")
	doc, _ := json.Marshal(map[string]string{"transcript_path": path})
	var out bytes.Buffer
	if err := run([]string{"hook"}, bytes.NewReader(doc), &out); err != nil || out.Len() != 0 {
		t.Fatalf("hook: err=%v out=%q", err, out.String())
	}
	data, err := os.ReadFile(filepath.Join(cacheDir(), lastErrorFile))
	if err != nil || !strings.Contains(string(data), "error: ") {
		t.Errorf("last error = %q, %v", data, err)
	}
}
