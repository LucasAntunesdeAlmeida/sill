package ledger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LucasAntunesdeAlmeida/sill/internal/cost"
)

func entry(session, repo string, written time.Time, usd float64) Entry {
	return Entry{
		Session: session, Repo: repo,
		Start: written.Add(-time.Hour), End: written, Written: written,
		USD: usd, Prices: cost.TableVersion,
		Models: cost.Totals{"claude-opus-5-5": {Tokens: cost.Tokens{Output: 100}, USD: usd}},
	}
}

var t0 = time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)

func TestAppendAndRead(t *testing.T) {
	dir := t.TempDir()
	if got, bad, err := Read(dir); err != nil || bad != 0 || len(got) != 0 {
		t.Fatalf("empty ledger = %v, %d, %v", got, bad, err)
	}
	if got, _, err := Read(filepath.Join(dir, "missing")); err != nil || len(got) != 0 {
		t.Fatalf("missing ledger = %v, %v", got, err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(Append(dir, entry("a", "/r1", t0, 1), entry("b", "/r1", t0.Add(time.Minute), 2)))
	// a is resumed and recorded again the next month, with larger totals.
	must(Append(dir, entry("a", "/r1", t0.AddDate(0, 1, 0), 5)))
	must(Append(dir, entry("c", "/r2", t0.Add(2*time.Minute), 10)))
	must(Append(dir)) // nothing to write

	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(files) != 2 {
		t.Errorf("month files = %v", files)
	}
	got, bad, err := Read(dir)
	if err != nil || bad != 0 {
		t.Fatal(err, bad)
	}
	var sessions []string
	for _, e := range got {
		sessions = append(sessions, fmt.Sprintf("%s=%g", e.Session, e.USD))
	}
	if strings.Join(sessions, " ") != "b=2 c=10 a=5" {
		t.Errorf("sessions = %v", sessions)
	}

	repos := ByRepo(got)
	if len(repos) != 2 || repos[0].Repo != "/r2" || repos[1].Repo != "/r1" || repos[1].Sessions != 2 ||
		repos[1].Cost.USD() != 7 || !repos[1].Last.Equal(t0.AddDate(0, 1, 0)) {
		t.Errorf("by repo = %+v", repos)
	}
}

// A split session counts in each repository with its share; a line without parts counts
// whole under its Repo.
func TestByRepoWithShares(t *testing.T) {
	part := func(repo string, usd float64) Part {
		return Part{Repo: repo, Models: cost.Totals{"claude-opus-5-5": {USD: usd}}}
	}
	split := entry("s1", "/beta", t0, 8)
	split.V, split.Parts = Version, []Part{part("/beta", 6), part("/alpha", 2)}
	old := entry("s0", "/alpha", t0.Add(-time.Hour), 1)
	repos := ByRepo([]Entry{split, old})
	got := map[string]string{}
	for _, r := range repos {
		got[r.Repo] = fmt.Sprintf("%d sessions $%g", r.Sessions, r.Cost.USD())
	}
	if got["/beta"] != "1 sessions $6" || got["/alpha"] != "2 sessions $3" || len(got) != 2 {
		t.Errorf("by repo = %v", got)
	}
	if len(old.Shares()) != 1 || old.Shares()[0].Repo != "/alpha" {
		t.Errorf("old shares = %+v", old.Shares())
	}
}

// A line cut short by a crash is skipped, and the next append starts on a line of its own.
func TestReadSurvivesABrokenLine(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, entry("a", "/r", t0, 1)); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "2026-09.jsonl")
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"session":"b","repo":"/r","us`)
	f.Close()
	if err := Append(dir, entry("c", "/r", t0.Add(time.Minute), 3)); err != nil {
		t.Fatal(err)
	}
	got, bad, err := Read(dir)
	if err != nil || bad != 1 || len(got) != 2 {
		t.Errorf("got %d entries, %d bad, %v", len(got), bad, err)
	}
}

// Sessions ending at the same moment append without losing a line.
func TestConcurrentAppends(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Append(dir, entry(fmt.Sprint("s", i), "/r", t0, float64(i))); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, bad, err := Read(dir)
	if err != nil || bad != 0 || len(got) != 50 {
		t.Errorf("got %d entries, %d bad, %v", len(got), bad, err)
	}
}
