package transcript

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/LucasAntunesdeAlmeida/sill/internal/atomicfile"
	"github.com/LucasAntunesdeAlmeida/sill/internal/cost"
)

// cacheVersion changes whenever what a scan counts changes, so a newer sill never trusts
// counts an older one saved.
const cacheVersion = 2

// headBytes of the transcript are fingerprinted to notice it was replaced or rewritten.
const headBytes = 4096

// cacheMaxAge is how long the state of a session nobody renders any more is kept.
const cacheMaxAge = 30 * 24 * time.Hour

// filePos is how far a file was read, with a fingerprint of its beginning to notice it
// was replaced or rewritten since.
type filePos struct {
	Offset   int64  `json:"offset"`    // bytes read, through the last complete line
	HeadLen  int    `json:"head_len"`  // bytes the fingerprint covers
	HeadHash string `json:"head_hash"` // sha256 of those bytes
}

// cacheState is where a scan stopped and what it had counted by then. Subagents run in
// files of their own next to the transcript; their positions and totals live here too, so
// a session keeps one state file however many agents it starts.
type cacheState struct {
	Version int    `json:"version"`
	Prices  string `json:"prices"` // cost.TableVersion the dollars were computed with
	filePos
	Compactions int                 `json:"compactions"`
	Start       time.Time           `json:"start"`
	Open        []string            `json:"open,omitempty"` // agents started, not yet finished
	Cost        cost.Totals         `json:"cost,omitempty"`
	Pending     *response           `json:"pending,omitempty"`
	Last        time.Time           `json:"last"`
	Cwd         string              `json:"cwd,omitempty"`
	Subagents   map[string]subState `json:"subagents,omitempty"` // by file name
	Seen        string              `json:"seen,omitempty"`      // seenSet.encode of every file
}

// subState is how far a subagent's file was read and what its responses cost.
type subState struct {
	filePos
	Cost    cost.Totals `json:"cost,omitempty"`
	Pending *response   `json:"pending,omitempty"`
	Last    time.Time   `json:"last"`
}

// ScanCached is Scan picking up where the previous render stopped: the scan state of each
// transcript is kept in cacheDir, so a render reads only what was appended since. A
// transcript that shrank or whose beginning changed is scanned again from the start. When
// ctx ends mid-scan, what was read is saved and counted and the next render goes on from
// there. An empty cacheDir scans the whole file every time.
//
// The subagents the session started are read the same way, for their cost only.
func ScanCached(ctx context.Context, path, cacheDir string) Activity {
	if path == "" {
		return Activity{}
	}
	f, err := os.Open(path)
	if err != nil {
		return Activity{}
	}
	defer f.Close()

	var st cacheState
	stateFile := ""
	if cacheDir != "" {
		stateFile = filepath.Join(cacheDir, cacheName(path))
		st = loadState(stateFile)
	}
	subs := subagentFiles(path)
	// The responses counted are shared by every file of the session, so one file that was
	// replaced means reading them all again.
	if st.Version != cacheVersion || st.Prices != cost.TableVersion || !st.fits(f) || !st.subagentsFit(subs) {
		st = cacheState{}
	}
	st.Version, st.Prices = cacheVersion, cost.TableVersion
	seen := decodeSeen(st.Seen)

	s := st.scanner(seen)
	consumed := scanFrom(ctx, f, st.Offset, s)
	changed := consumed > 0
	if changed {
		st.Offset += consumed
		st.record(s)
		st.fingerprint(f)
	}
	act := s.activity()

	names := make([]string, 0, len(subs))
	for name := range subs {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if ctx.Err() != nil {
			break
		}
		sub, known := st.Subagents[name]
		if n, scanned := scanSubagent(ctx, subs[name], &sub, seen); scanned && (n > 0 || !known) {
			changed = true
			if st.Subagents == nil {
				st.Subagents = map[string]subState{}
			}
			st.Subagents[name] = sub
		}
	}
	// Every subagent seen so far counts, even one whose file is gone.
	for _, sub := range st.Subagents {
		sc := sub.scanner(seen)
		if t := sc.totals(); t != nil {
			if act.Cost == nil {
				act.Cost = cost.Totals{}
			}
			act.Cost.Merge(t)
		}
		if sub.Last.After(act.Last) {
			act.Last = sub.Last
		}
	}

	if stateFile != "" && changed {
		st.Seen = seen.encode()
		saveState(cacheDir, stateFile, st)
	}
	return act
}

// subagentsFit reports whether every subagent file read before still matches its saved
// position. A file that is gone does not count against it: its responses stay counted.
func (st cacheState) subagentsFit(files map[string]string) bool {
	for name, sub := range st.Subagents {
		path, ok := files[name]
		if !ok {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		fits := sub.Offset == 0 || sub.filePos.fits(f)
		f.Close()
		if !fits {
			return false
		}
	}
	return true
}

// scanFrom reads records from offset on into s until ctx ends, and returns the bytes of
// complete lines read.
func scanFrom(ctx context.Context, f *os.File, offset int64, s *scanner) int64 {
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0
	}
	n := 0
	consumed, _ := readLines(f, false, func(line []byte) bool {
		if n%64 == 0 && ctx.Err() != nil {
			return false
		}
		n++
		s.record(line)
		return true
	})
	return consumed
}

// subagentFiles lists the transcripts of the subagents a session started, by file name.
// They live in <session>/subagents/ next to <session>.jsonl.
func subagentFiles(path string) map[string]string {
	dir := filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	files := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			files[e.Name()] = filepath.Join(dir, e.Name())
		}
	}
	return files
}

// scanSubagent reads what was appended to a subagent's file into sub, and returns the
// bytes read and whether the file could be read at all.
func scanSubagent(ctx context.Context, path string, sub *subState, seen seenSet) (int64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	s := sub.scanner(seen)
	n := scanFrom(ctx, f, sub.Offset, s)
	if n > 0 {
		sub.Offset += n
		sub.Cost, sub.Pending, sub.Last = s.cost, pendingPtr(s.pending), s.last
		sub.fingerprint(f)
	}
	return n, true
}

func (sub subState) scanner(seen seenSet) *scanner {
	s := &scanner{cost: sub.Cost, last: sub.Last, seen: seen}
	if sub.Pending != nil {
		s.pending = *sub.Pending
	}
	return s
}

func pendingPtr(r response) *response {
	if r.ID == "" {
		return nil
	}
	return &r
}

// fits reports whether the saved position still describes f: the file has not shrunk
// below the offset and its beginning is unchanged.
func (p filePos) fits(f *os.File) bool {
	if p.Offset <= 0 {
		return false
	}
	fi, err := f.Stat()
	if err != nil || fi.Size() < p.Offset {
		return false
	}
	head, err := readHead(f, p.HeadLen)
	return err == nil && len(head) == p.HeadLen && hashHex(head) == p.HeadHash
}

func (p *filePos) fingerprint(f *os.File) {
	head, err := readHead(f, headBytes)
	if err != nil {
		p.HeadLen, p.HeadHash = 0, ""
		return
	}
	p.HeadLen, p.HeadHash = len(head), hashHex(head)
}

func (st cacheState) scanner(seen seenSet) *scanner {
	s := &scanner{compactions: st.Compactions, start: st.Start, cost: st.Cost, last: st.Last, cwd: st.Cwd, seen: seen}
	if st.Pending != nil {
		s.pending = *st.Pending
	}
	for _, id := range st.Open {
		if s.open == nil {
			s.open = map[string]bool{}
		}
		s.open[id] = true
	}
	return s
}

func (st *cacheState) record(s *scanner) {
	st.Compactions, st.Start, st.Open = s.compactions, s.start, nil
	st.Cost, st.Pending, st.Last, st.Cwd = s.cost, pendingPtr(s.pending), s.last, s.cwd
	for id := range s.open {
		st.Open = append(st.Open, id)
	}
	slices.Sort(st.Open)
}

// readHead reads up to n bytes from the start of f without moving its offset.
func readHead(f *os.File, n int) ([]byte, error) {
	buf := make([]byte, n)
	got, err := f.ReadAt(buf, 0)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf[:got], nil
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// cacheName is the state file for a transcript path.
func cacheName(path string) string {
	return hashHex([]byte(path))[:32] + ".json"
}

func loadState(file string) cacheState {
	var st cacheState
	data, err := os.ReadFile(file)
	if err != nil || json.Unmarshal(data, &st) != nil {
		return cacheState{}
	}
	return st
}

// saveState writes the state, and the first time for a session clears out the state of
// sessions not rendered for a month so the folder does not grow without bound.
func saveState(dir, file string, st cacheState) {
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	if _, err := os.Stat(file); err != nil {
		if os.MkdirAll(dir, 0o755) != nil {
			return
		}
		pruneStates(dir, time.Now().Add(-cacheMaxAge))
	}
	_ = atomicfile.Write(file, data, 0o644)
}

func pruneStates(dir string, before time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if fi, err := e.Info(); err == nil && fi.ModTime().Before(before) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
