package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCostReport(t *testing.T) {
	projects := hookEnv(t)
	var out bytes.Buffer
	if err := run([]string{"cost"}, nil, &out); err != nil || !strings.Contains(out.String(), "No session costs recorded yet") {
		t.Fatalf("empty: %v\n%s", err, out.String())
	}

	repoA, repoB := filepath.Join(t.TempDir(), "alpha"), filepath.Join(t.TempDir(), "beta")
	for _, r := range []string{repoA, repoB} {
		if err := os.MkdirAll(filepath.Join(r, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Two sessions in A, one of them with a model sill has no price for; one in B.
	writeFile(t, filepath.Join(projects, "a", "a1.jsonl"), transcriptDoc(repoA, 1_000_000))
	writeFile(t, filepath.Join(projects, "a", "a2.jsonl"),
		strings.Replace(transcriptDoc(repoA, 2000), "claude-opus-5-5", "claude-future-9", 1))
	writeFile(t, filepath.Join(projects, "b", "b1.jsonl"), transcriptDoc(repoB, 10_000))

	// The report records what the ledger lacks, the way the hook would.
	out.Reset()
	if err := run([]string{"cost"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out.String(), "\n")
	// Output tokens cost $20 per million on Opus 5.5.
	want := []string{
		"repo sessions in out cost last",
		"/alpha 2 0 1m $20.00+ 2026-09-29",
		"/beta 1 0 10k $0.20 2026-09-29",
		"total 3 0 1m $20.20+",
	}
	for i, w := range want {
		if i >= len(lines) || !strings.HasSuffix(collapse(lines[i]), w) {
			t.Errorf("line %d = %q, want it to end in %q\n%s", i, lines[i], w, out.String())
		}
	}
	if !strings.Contains(out.String(), "came from models without a price") || !strings.Contains(out.String(), "not what a Pro or Max plan bills") {
		t.Errorf("footer missing:\n%s", out.String())
	}

	// One repository, from a folder inside it.
	sub := filepath.Join(repoA, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"cost", sub}, nil, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, w := range []string{"$20.00  opus-5-5", "-  future-9", "a1", "a2", "2 session(s)"} {
		if !strings.Contains(collapse(text), collapse(w)) {
			t.Errorf("repo report lacks %q:\n%s", w, text)
		}
	}
	if strings.Contains(text, "b1") {
		t.Errorf("another repository's session shown:\n%s", text)
	}

	out.Reset()
	if err := run([]string{"cost", t.TempDir()}, nil, &out); err != nil || !strings.Contains(out.String(), "No session costs recorded for") {
		t.Errorf("unknown repo: %v\n%s", err, out.String())
	}
	if err := run([]string{"cost", "a", "b"}, nil, &out); err == nil {
		t.Error("two folders accepted")
	}
}

// collapse squeezes runs of spaces, so a test does not depend on column widths.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }
