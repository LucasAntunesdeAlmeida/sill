// Package ledger keeps what each session cost after Claude Code deletes its transcript:
// one line per session in a file per month. Lines are only ever appended. A session can
// be written again with larger totals (it was resumed, or it was first recorded while
// still running); the line written last wins.
//
// This is the one file sill does not replace through atomicfile: two sessions ending at
// the same moment would each rewrite the month and one line would be lost. Appends of a
// few lines at once do not interleave, on Unix or Windows.
package ledger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/LucasAntunesdeAlmeida/sill/internal/cost"
)

// DirName is the ledger folder inside the Claude config directory.
const DirName = "sill-costs"

// Version is the format of the lines written now. 2 splits a session between the
// repositories its responses were made in; a line without a version put a whole session
// under the directory of its latest response.
const Version = 2

// Entry is one session's line.
type Entry struct {
	V        int         `json:"v,omitempty"`
	Session  string      `json:"session"`
	Repo     string      `json:"repo"`            // where most of the cost went: a repository folder, or the working directory
	Parts    []Part      `json:"parts,omitempty"` // every repository's share, when there is more than one
	Cwd      string      `json:"cwd,omitempty"`
	Start    time.Time   `json:"start"`
	End      time.Time   `json:"end"` // the latest response
	Written  time.Time   `json:"written"`
	USD      float64     `json:"usd"`
	Unpriced int64       `json:"unpriced,omitempty"` // tokens without a price, not in USD
	Prices   string      `json:"prices"`             // the price table the dollars came from
	Models   cost.Totals `json:"models"`             // the whole session
}

// Part is the share of a session made in one repository.
type Part struct {
	Repo   string      `json:"repo"`
	Models cost.Totals `json:"models"`
}

// Shares is how the session divides between repositories: its parts, or all of it in Repo.
func (e Entry) Shares() []Part {
	if len(e.Parts) > 0 {
		return e.Parts
	}
	return []Part{{Repo: e.Repo, Models: e.Models}}
}

// Append writes entries to the month file of their Written time, one write per file.
func Append(dir string, entries ...Entry) error {
	if len(entries) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	byMonth := map[string][]byte{}
	var months []string
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		m := e.Written.UTC().Format("2006-01")
		if _, ok := byMonth[m]; !ok {
			months = append(months, m)
		}
		byMonth[m] = append(append(byMonth[m], line...), '\n')
	}
	for _, m := range months {
		if err := appendFile(filepath.Join(dir, m+".jsonl"), byMonth[m]); err != nil {
			return err
		}
	}
	return nil
}

// appendFile adds data at the end of path in a single write. A last line left without
// its newline by a crash is closed first, so it does not swallow the new one.
func appendFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, fi.Size()-1); err == nil && last[0] != '\n' {
			data = append([]byte{'\n'}, data...)
		}
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Read returns the latest entry of every session in the ledger, oldest session first, and
// how many lines could not be read. A missing ledger is empty.
func Read(dir string) (entries []Entry, bad int, err error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, 0, err
	}
	latest := map[string]Entry{}
	for _, file := range files {
		f, err := os.Open(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, bad, err
		}
		n, err := readFile(f, latest)
		f.Close()
		bad += n
		if err != nil {
			return nil, bad, err
		}
	}
	for _, e := range latest {
		entries = append(entries, e)
	}
	slices.SortFunc(entries, func(a, b Entry) int {
		if c := a.Start.Compare(b.Start); c != 0 {
			return c
		}
		return strings.Compare(a.Session, b.Session)
	})
	return entries, bad, nil
}

func readFile(r io.Reader, latest map[string]Entry) (bad int, err error) {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var e Entry
			if json.Unmarshal(line, &e) != nil || e.Session == "" {
				bad++
			} else if prev, ok := latest[e.Session]; !ok || !e.Written.Before(prev.Written) {
				latest[e.Session] = e
			}
		}
		if err == io.EOF {
			return bad, nil
		}
		if err != nil {
			return bad, err
		}
	}
}

// Repo is the sessions of one repository added up.
type Repo struct {
	Repo     string
	Sessions int // sessions with a share here

	Last time.Time // the latest response of any of its sessions
	Cost cost.Totals
}

// ByRepo adds up entries per repository, most expensive first. A session split between
// repositories counts in each, with its share there.
func ByRepo(entries []Entry) []Repo {
	byName := map[string]*Repo{}
	for _, e := range entries {
		for _, p := range e.Shares() {
			r, ok := byName[p.Repo]
			if !ok {
				r = &Repo{Repo: p.Repo, Cost: cost.Totals{}}
				byName[p.Repo] = r
			}
			r.Sessions++
			r.Cost.Merge(p.Models)
			if e.End.After(r.Last) {
				r.Last = e.End
			}
		}
	}
	repos := make([]Repo, 0, len(byName))
	for _, r := range byName {
		repos = append(repos, *r)
	}
	slices.SortFunc(repos, func(a, b Repo) int {
		if ua, ub := a.Cost.USD(), b.Cost.USD(); ua != ub {
			if ua > ub {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Repo, b.Repo)
	})
	return repos
}
