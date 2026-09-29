package transcript

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/LucasAntunesdeAlmeida/sill/internal/cost"
)

func sessionData(t testing.TB) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// same compares activities; a time read back from the cache is the same instant in a
// different *time.Location, so == would not do.
func same(a, b Activity) bool {
	return a.Agents == b.Agents && a.Compactions == b.Compactions && a.Start.Equal(b.Start) &&
		a.Last.Equal(b.Last) && a.Cwd == b.Cwd && sameCost(a.Cost, b.Cost)
}

// sameCost compares totals, dollars within float rounding.
func sameCost(a, b cost.Totals) bool {
	if len(a) != len(b) {
		return false
	}
	for k, u := range a {
		v, ok := b[k]
		if !ok || u.Tokens != v.Tokens || u.Unpriced != v.Unpriced || math.Abs(u.USD-v.USD) > 1e-9 {
			return false
		}
	}
	return true
}

func appendFile(t testing.TB, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
}

// Feeding the transcript in pieces, including a cut in the middle of a record, gives the
// same counts as one full scan, and each render only reads what is new.
func TestScanCachedIncremental(t *testing.T) {
	data := sessionData(t)
	want := ScanReader(bytes.NewReader(data))
	dir, cache := t.TempDir(), t.TempDir()
	path := filepath.Join(dir, "s.jsonl")

	cut := bytes.Index(data, []byte("toolu_D")) // inside the record launching D and E
	appendFile(t, path, data[:cut])
	first := ScanCached(context.Background(), path, cache)
	if first.Agents != 1 || first.Compactions != 1 { // B is open; the cut record is not read yet
		t.Errorf("first part = %+v", first)
	}
	st := loadState(filepath.Join(cache, cacheName(path)))
	if int(st.Offset) != bytes.LastIndexByte(data[:cut], '\n')+1 {
		t.Errorf("offset %d should stop at the last complete line", st.Offset)
	}

	appendFile(t, path, data[cut:])
	if got := ScanCached(context.Background(), path, cache); !same(got, want) {
		t.Errorf("after the rest: %+v, want %+v", got, want)
	}
	if got := ScanCached(context.Background(), path, cache); !same(got, want) {
		t.Errorf("nothing new: %+v, want %+v", got, want)
	}
	if got := ScanCached(context.Background(), path, ""); !same(got, want) {
		t.Errorf("without a cache: %+v, want %+v", got, want)
	}
}

func TestScanCachedRescansAReplacedFile(t *testing.T) {
	dir, cache := t.TempDir(), t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	compact := `{"type":"system","subtype":"compact_boundary","timestamp":"2026-09-25T10:00:00Z"}` + "\n"
	appendFile(t, path, []byte(strings.Repeat(compact, 3)))
	if got := ScanCached(context.Background(), path, cache); got.Compactions != 3 {
		t.Fatalf("first = %+v", got)
	}
	// Rewritten with different content but more bytes than before.
	other := strings.Replace(compact, "10:00:00", "11:00:00", 1)
	if err := os.WriteFile(path, []byte(strings.Repeat(other, 4)), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ScanCached(context.Background(), path, cache); got.Compactions != 4 {
		t.Errorf("replaced file = %+v, want a fresh count of 4", got)
	}
	// Truncated below the saved offset.
	if err := os.WriteFile(path, []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ScanCached(context.Background(), path, cache); got.Compactions != 1 {
		t.Errorf("truncated file = %+v, want 1", got)
	}
	// A state from another sill version is not trusted.
	stateFile := filepath.Join(cache, cacheName(path))
	st := loadState(stateFile)
	st.Version, st.Compactions = cacheVersion+1, 99
	saveState(cache, stateFile, st)
	if got := ScanCached(context.Background(), path, cache); got.Compactions != 1 {
		t.Errorf("foreign version = %+v, want 1", got)
	}
}

// A render out of time returns what the cache has and the next one continues.
func TestScanCachedResumesAfterDeadline(t *testing.T) {
	dir, cache := t.TempDir(), t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	compact := `{"type":"system","subtype":"compact_boundary"}` + "\n"
	appendFile(t, path, []byte(strings.Repeat(compact, 1000)))

	done, cancel := context.WithCancel(context.Background())
	cancel()
	if got := ScanCached(done, path, cache); got.Compactions != 0 {
		t.Errorf("expired before starting = %+v", got)
	}
	if got := ScanCached(context.Background(), path, cache); got.Compactions != 1000 {
		t.Errorf("next render = %+v", got)
	}
}

func TestScanCachedMissing(t *testing.T) {
	if got := ScanCached(context.Background(), "", t.TempDir()); !got.Empty() {
		t.Errorf("no path = %+v", got)
	}
	if got := ScanCached(context.Background(), filepath.Join(t.TempDir(), "gone.jsonl"), t.TempDir()); !got.Empty() {
		t.Errorf("missing file = %+v", got)
	}
}

func TestPruneStates(t *testing.T) {
	dir := t.TempDir()
	old, fresh := filepath.Join(dir, "old.json"), filepath.Join(dir, "fresh.json")
	other := filepath.Join(dir, "notes.txt")
	for _, p := range []string{old, fresh, other} {
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-2 * cacheMaxAge)
	for _, p := range []string{old, other} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	pruneStates(dir, time.Now().Add(-cacheMaxAge))
	for p, want := range map[string]bool{old: false, fresh: true, other: true} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", filepath.Base(p), err == nil, want)
		}
	}
}

// FuzzScanSplit: however the transcript is split across renders, the cached result equals
// one full scan.
func FuzzScanSplit(f *testing.F) {
	f.Add(sessionData(f), 500, 1200)
	f.Add(usageData(f), 700, 2100)
	f.Add([]byte("{\"subtype\":\"compact_boundary\"}\n\n{}\n"), 3, 3)
	f.Fuzz(func(t *testing.T, data []byte, a, b int) {
		if !utf8.Valid(data) {
			return // a transcript is JSON, so UTF-8; agent ids are kept as JSON strings
		}
		if len(data) == 0 || data[len(data)-1] != '\n' {
			data = append(data, '\n') // a complete transcript ends with a newline
		}
		clamp := func(i int) int {
			if i < 0 {
				i = -i
			}
			return i % (len(data) + 1)
		}
		a, b = clamp(a), clamp(b)
		if a > b {
			a, b = b, a
		}
		path := filepath.Join(t.TempDir(), "s.jsonl")
		cache := t.TempDir()
		for _, part := range [][]byte{data[:a], data[a:b], data[b:]} {
			appendFile(t, path, part)
			ScanCached(context.Background(), path, cache)
		}
		got := ScanCached(context.Background(), path, cache)
		if want := ScanReader(bytes.NewReader(data)); !same(got, want) {
			t.Fatalf("split at %d,%d: %+v, want %+v", a, b, got, want)
		}
	})
}

// syntheticTranscript is about size bytes of realistic records with agents and compactions.
func syntheticTranscript(tb testing.TB, size int) []byte {
	tb.Helper()
	base := sessionData(tb)
	filler := `{"type":"user","message":{"content":[{"type":"tool_result","content":"` + strings.Repeat("filler ", 2000) + `"}]}}` + "\n"
	var b bytes.Buffer
	for i := 0; b.Len() < size; i++ {
		b.Write(bytes.ReplaceAll(base, []byte("toolu_"), fmt.Appendf(nil, "toolu_%d_", i)))
		b.WriteString(filler)
	}
	return b.Bytes()
}

func BenchmarkScanReader(b *testing.B) {
	data := syntheticTranscript(b, 20<<20)
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		ScanReader(bytes.NewReader(data))
	}
}

// BenchmarkScanCachedNoChange is a render when nothing was appended: the common case.
func BenchmarkScanCachedNoChange(b *testing.B) {
	path := filepath.Join(b.TempDir(), "s.jsonl")
	appendFile(b, path, syntheticTranscript(b, 20<<20))
	cache := b.TempDir()
	ScanCached(context.Background(), path, cache)
	for b.Loop() {
		ScanCached(context.Background(), path, cache)
	}
}
