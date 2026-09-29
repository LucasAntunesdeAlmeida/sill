package transcript

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LucasAntunesdeAlmeida/sill/internal/cost"
)

func usageData(t testing.TB) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "usage.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// mainCost is what testdata/usage.jsonl cost: each response once, at its final usage.
func mainCost() cost.Totals {
	want := cost.Totals{}
	// msg_1: two records, the second has the final output count. The tool input's own
	// "usage" object and the iterations list are not the response's usage.
	want.Add("claude-opus-5-5", false, at("2026-09-25T10:00:06Z"),
		cost.Tokens{Input: 2, Output: 60, CacheRead: 1000, CacheWrite1h: 100})
	// msg_2: no TTL breakdown, so the writes count as 5 minute ones.
	want.Add("claude-haiku-4-5-20251001", false, at("2026-09-25T10:01:00Z"),
		cost.Tokens{Input: 5, Output: 10, CacheWrite5m: 200})
	// The <synthetic> record is not a response. msg_3 ran in fast mode.
	want.Add("claude-opus-5-5", true, at("2026-09-25T10:03:00Z"), cost.Tokens{Input: 3, Output: 100})
	// msg_4 is still pending at the end of the file, and its model has no price.
	want.Add("claude-future-9", false, at("2026-09-25T10:04:00Z"), cost.Tokens{Input: 10, Output: 10})
	return want
}

// subagentCost is what the subagents in testdata/usage/subagents/ add: a1's response, and
// the one response of f1 that is its own. f1 is a fork, so its file starts with copies of
// msg_1 and msg_s1, which were counted already.
func subagentCost() cost.Totals {
	want := a1Cost()
	want.Add("claude-sonnet-5-5", false, at("2026-09-25T10:07:00Z"), cost.Tokens{Input: 3, Output: 7, CacheRead: 800})
	return want
}

func a1Cost() cost.Totals {
	want := cost.Totals{}
	want.Add("claude-sonnet-5-5", false, at("2026-09-25T10:05:00Z"), cost.Tokens{Input: 4, Output: 20, CacheRead: 500})
	return want
}

func TestScanCountsEachResponseOnce(t *testing.T) {
	act := ScanReader(strings.NewReader(string(usageData(t))))
	if want := mainCost(); !sameCost(act.Cost, want) {
		t.Errorf("cost = %+v\nwant  %+v", act.Cost, want)
	}
	if act.Cost.Unpriced() != 20 {
		t.Errorf("unpriced = %d, want the 20 tokens of claude-future-9", act.Cost.Unpriced())
	}
	if _, ok := act.Cost["claude-opus-5-5/fast"]; !ok {
		t.Errorf("fast mode not kept apart: %v", act.Cost.Models())
	}
	if !act.Last.Equal(at("2026-09-25T10:04:00Z")) {
		t.Errorf("last = %v", act.Last)
	}
	if act.Cwd != `C:\work\repo\sub` {
		t.Errorf("cwd = %q", act.Cwd)
	}
}

// The subagent in usage/subagents/ adds its responses to the session's.
func TestScanCachedIncludesSubagents(t *testing.T) {
	want := mainCost()
	want.Merge(subagentCost())
	cache := t.TempDir()
	for _, run := range []string{"first", "cached"} {
		act := ScanCached(context.Background(), filepath.Join("testdata", "usage.jsonl"), cache)
		if !sameCost(act.Cost, want) {
			t.Errorf("%s: cost = %+v\nwant  %+v", run, act.Cost, want)
		}
		if !act.Last.Equal(at("2026-09-25T10:07:00Z")) {
			t.Errorf("%s: last = %v, want the fork's own response", run, act.Last)
		}
	}
}

// A subagent file rewritten from the start makes the whole session count again, so the
// responses it shared with other files are neither lost nor doubled.
func TestReplacedSubagentRescansTheSession(t *testing.T) {
	dir, cache := t.TempDir(), t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	appendFile(t, path, usageData(t))
	subDir := filepath.Join(dir, "s", "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fork, err := os.ReadFile(filepath.Join("testdata", "usage", "subagents", "agent-f1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	forkPath := filepath.Join(subDir, "agent-f1.jsonl")
	appendFile(t, forkPath, fork)
	ScanCached(context.Background(), path, cache)

	// Rewritten: the copy of msg_1 is gone and the fork's own response is different.
	lines := strings.SplitAfter(string(fork), "\n")
	rewritten := lines[0] + strings.Replace(lines[4], `"output_tokens":7`, `"output_tokens":9`, 1)
	if err := os.WriteFile(forkPath, []byte(rewritten), 0o644); err != nil {
		t.Fatal(err)
	}
	want := mainCost()
	want.Add("claude-sonnet-5-5", false, at("2026-09-25T10:07:00Z"), cost.Tokens{Input: 3, Output: 9, CacheRead: 800})
	if got := ScanCached(context.Background(), path, cache); !sameCost(got.Cost, want) {
		t.Errorf("after the rewrite = %+v\nwant %+v", got.Cost, want)
	}
}

func TestSeenSet(t *testing.T) {
	s := seenSet{}
	resp := func(id string, out int64) response {
		return response{ID: id, Tokens: cost.Tokens{Input: 5, CacheRead: 100, Output: out}}
	}
	for _, id := range []string{"msg_1", "msg_2", "msg_3"} {
		if tok, ok := s.add(resp(id, 50)); !ok || tok != resp(id, 50).Tokens {
			t.Errorf("%s: added %+v, %v", id, tok, ok)
		}
	}
	if _, ok := s.add(resp("msg_2", 50)); ok {
		t.Error("msg_2 counted twice")
	}
	if _, ok := s.add(resp("msg_2", 40)); ok {
		t.Error("a smaller copy of msg_2 counted")
	}
	// A copy taken while the response was being written, then the final record.
	if tok, ok := s.add(resp("msg_2", 60)); !ok || tok != (cost.Tokens{Output: 10}) {
		t.Errorf("the rest of msg_2 = %+v, %v", tok, ok)
	}
	back := decodeSeen(s.encode())
	if len(back) != 3 || back[idHash("msg_2")] != 60 || back[idHash("msg_1")] != 50 {
		t.Errorf("round trip = %v", back)
	}
	if len(decodeSeen("not base64!")) != 0 || len(decodeSeen("")) != 0 {
		t.Error("bad input should decode to an empty set")
	}
}

// A fork can copy the response that started it before that response is complete. The
// response counts at its final size whichever file is read first.
func TestPartialCopyInAFork(t *testing.T) {
	record := func(id string, out int) string {
		return fmt.Sprintf(`{"message":{"model":"claude-opus-5-5","id":"%s","usage":{"input_tokens":1,"output_tokens":%d}},"timestamp":"2026-09-25T10:00:00Z"}`+"\n", id, out)
	}
	want := cost.Totals{}
	want.Add("claude-opus-5-5", false, at("2026-09-25T10:00:00Z"), cost.Tokens{Input: 1, Output: 60})
	want.Add("claude-opus-5-5", false, at("2026-09-25T10:00:00Z"), cost.Tokens{Input: 1, Output: 5})
	want.Add("claude-opus-5-5", false, at("2026-09-25T10:00:00Z"), cost.Tokens{Input: 1, Output: 7})
	// The parent's file is read before the fork's in one run and after it in the other.
	for _, names := range [][2]string{{"agent-a.jsonl", "agent-b.jsonl"}, {"agent-b.jsonl", "agent-a.jsonl"}} {
		dir := t.TempDir()
		path := filepath.Join(dir, "s.jsonl")
		appendFile(t, path, []byte(`{"type":"user","message":{"content":"hi"}}`+"\n"))
		subDir := filepath.Join(dir, "s", "subagents")
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			t.Fatal(err)
		}
		parent, fork := names[0], names[1]
		appendFile(t, filepath.Join(subDir, parent), []byte(record("msg_p", 60)+record("msg_p2", 5)))
		appendFile(t, filepath.Join(subDir, fork), []byte(record("msg_p", 20)+record("msg_f", 7)))
		if got := ScanCached(context.Background(), path, t.TempDir()); !sameCost(got.Cost, want) {
			t.Errorf("parent %s, fork %s: %+v\nwant %+v", parent, fork, got.Cost, want)
		}
	}
}

// A subagent that is still writing is picked up where it stopped, and its partial
// response is replaced, not added, once the final record arrives.
func TestSubagentIncremental(t *testing.T) {
	dir, cache := t.TempDir(), t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	appendFile(t, path, usageData(t))
	subDir := filepath.Join(dir, "s", "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sub, err := os.ReadFile(filepath.Join("testdata", "usage", "subagents", "agent-a1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(sub), "\n")
	subPath := filepath.Join(subDir, "agent-a1.jsonl")

	appendFile(t, subPath, []byte(lines[0]+lines[1])) // the partial record only
	partial := mainCost()
	partial.Add("claude-sonnet-5-5", false, at("2026-09-25T10:00:20Z"), cost.Tokens{Input: 4, Output: 5, CacheRead: 500})
	if got := ScanCached(context.Background(), path, cache); !sameCost(got.Cost, partial) {
		t.Errorf("partial = %+v", got.Cost)
	}

	appendFile(t, subPath, []byte(lines[2]))
	full := mainCost()
	full.Merge(a1Cost())
	if got := ScanCached(context.Background(), path, cache); !sameCost(got.Cost, full) {
		t.Errorf("after the final record = %+v", got.Cost)
	}
	st := loadState(filepath.Join(cache, cacheName(path)))
	if s, ok := st.Subagents["agent-a1.jsonl"]; !ok || s.Offset != int64(len(sub)) {
		t.Errorf("subagent state = %+v", st.Subagents)
	}

	// The subagent's file going away does not take its cost with it.
	if err := os.RemoveAll(filepath.Join(dir, "s")); err != nil {
		t.Fatal(err)
	}
	if got := ScanCached(context.Background(), path, cache); !sameCost(got.Cost, full) {
		t.Errorf("after the file is gone = %+v", got.Cost)
	}
}

// State saved with another price table is recomputed, so dollars never mix two tables.
func TestScanCachedRepricesOnNewTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	appendFile(t, path, usageData(t))
	cache := t.TempDir()
	ScanCached(context.Background(), path, cache)
	stateFile := filepath.Join(cache, cacheName(path))
	st := loadState(stateFile)
	u := st.Cost["claude-opus-5-5"]
	u.USD = 1000
	st.Cost["claude-opus-5-5"] = u
	st.Prices = "an-old-table"
	saveState(cache, stateFile, st)
	if got := ScanCached(context.Background(), path, cache); !sameCost(got.Cost, mainCost()) {
		t.Errorf("stale dollars kept: %+v", got.Cost)
	}
}

func TestParseResponse(t *testing.T) {
	if _, ok := parseResponse([]byte(`{"type":"user","message":{"role":"user","content":"x"}}`)); ok {
		t.Error("a user record is not a response")
	}
	if _, ok := parseResponse([]byte(`{"message":{"model":"claude-x","id":"m","usage":{"input_tokens":1`)); ok {
		t.Error("a cut usage object is not a response")
	}
	r, ok := parseResponse([]byte(`{"message":{"model":"claude-x","id":"m","usage":{"input_tokens":7,"output_tokens":"x"}}}`))
	if !ok || r.Tokens.Input != 7 || r.Tokens.Output != 0 || !r.At.IsZero() {
		t.Errorf("odd values = %+v, %v", r, ok)
	}
}

func TestObject(t *testing.T) {
	for in, want := range map[string]string{
		`{"a":1},"b":2}`:         `{"a":1}`,
		`{"a":"}{\"}","b":{}}x`:  `{"a":"}{\"}","b":{}}`,
		`{"a":[{"b":1}],"c":2}]`: `{"a":[{"b":1}],"c":2}`,
		`{"a":1`:                 ``,
		`[1]`:                    ``,
		``:                       ``,
	} {
		if got := string(object([]byte(in))); got != want {
			t.Errorf("object(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestLastString(t *testing.T) {
	line := []byte(`{"cwd":"first","x":{"cwd":"C:\\a \"b\"\u0041"}}`)
	if got := lastString(line, cwdMarker); got != `C:\a "b"A` {
		t.Errorf("lastString = %q", got)
	}
	if got := lastString([]byte(`{"cwd":"unterminated`), cwdMarker); got != "" {
		t.Errorf("unterminated = %q", got)
	}
}

func TestInspectResponses(t *testing.T) {
	rep, err := Inspect(filepath.Join("testdata", "usage.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// Five records of four responses; the synthetic record has no claude- model.
	if rep.Responses != 5 || rep.LooseResponses != 5 || len(rep.Drift()) != 0 {
		t.Errorf("report = %+v, drift %q", rep, rep.Drift())
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	doc := `{"message": {"model": "claude-x", "id": "m", "usage": {"input_tokens": 1}}, "timestamp":"2026-09-25T10:00:00Z"}` + "\n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err = Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rep.Drift(), "|"); got != "1 of 1 responses not recognised" {
		t.Errorf("drift = %q", got)
	}
}
