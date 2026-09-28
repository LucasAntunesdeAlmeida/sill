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
)

// cacheVersion changes whenever what a scan counts changes, so a newer sill never trusts
// counts an older one saved.
const cacheVersion = 1

// headBytes of the transcript are fingerprinted to notice it was replaced or rewritten.
const headBytes = 4096

// cacheMaxAge is how long the state of a session nobody renders any more is kept.
const cacheMaxAge = 30 * 24 * time.Hour

// cacheState is where a scan stopped and what it had counted by then.
type cacheState struct {
	Version     int       `json:"version"`
	Offset      int64     `json:"offset"`    // bytes read, through the last complete line
	HeadLen     int       `json:"head_len"`  // bytes the fingerprint covers
	HeadHash    string    `json:"head_hash"` // sha256 of those bytes
	Compactions int       `json:"compactions"`
	Start       time.Time `json:"start"`
	Open        []string  `json:"open,omitempty"` // agents started, not yet finished
}

// ScanCached is Scan picking up where the previous render stopped: the scan state of each
// transcript is kept in cacheDir, so a render reads only what was appended since. A
// transcript that shrank or whose beginning changed is scanned again from the start. When
// ctx ends mid-scan, what was read is saved and counted and the next render goes on from
// there. An empty cacheDir scans the whole file every time.
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
	if !st.fits(f) {
		st = cacheState{Version: cacheVersion}
	}
	if _, err := f.Seek(st.Offset, io.SeekStart); err != nil {
		return Activity{}
	}

	s := st.scanner()
	n := 0
	consumed, _ := readLines(f, false, func(line []byte) bool {
		if n%64 == 0 && ctx.Err() != nil {
			return false
		}
		n++
		s.record(line)
		return true
	})
	if stateFile != "" && consumed > 0 {
		st.Offset += consumed
		st.record(s)
		st.fingerprint(f)
		saveState(cacheDir, stateFile, st)
	}
	return s.activity()
}

// fits reports whether the saved state still describes f: same version, the file has not
// shrunk below the offset, and its beginning is unchanged.
func (st cacheState) fits(f *os.File) bool {
	if st.Version != cacheVersion || st.Offset <= 0 {
		return false
	}
	fi, err := f.Stat()
	if err != nil || fi.Size() < st.Offset {
		return false
	}
	head, err := readHead(f, st.HeadLen)
	return err == nil && len(head) == st.HeadLen && hashHex(head) == st.HeadHash
}

func (st *cacheState) fingerprint(f *os.File) {
	head, err := readHead(f, headBytes)
	if err != nil {
		st.HeadLen, st.HeadHash = 0, ""
		return
	}
	st.HeadLen, st.HeadHash = len(head), hashHex(head)
}

func (st cacheState) scanner() *scanner {
	s := &scanner{compactions: st.Compactions, start: st.Start}
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
