package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/LucasAntunesdeAlmeida/sill/internal/config"
	"github.com/LucasAntunesdeAlmeida/sill/internal/cost"
	"github.com/LucasAntunesdeAlmeida/sill/internal/gitinfo"
	"github.com/LucasAntunesdeAlmeida/sill/internal/ledger"
	"github.com/LucasAntunesdeAlmeida/sill/internal/transcript"
)

// hookInput is what Claude Code pipes to a hook; only the fields sill uses.
type hookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
}

// hookBudget bounds a hook run: recording the session that ended and catching up on the
// others. A first run over a month of transcripts may need several sessions to finish.
const hookBudget = 10 * time.Second

// sweepAge is how far back a sweep looks: Claude Code deletes transcripts after 30 days by
// default, so an older one is either gone or already recorded.
const sweepAge = 30 * 24 * time.Hour

// hook runs as Claude Code's SessionEnd hook. It records the session that ended, then
// sweeps for sessions that ended without the hook (a killed terminal) or grew since they
// were recorded. It never fails the hook: problems go to last-error.txt for sill doctor.
func hook(stdin io.Reader) {
	if err := recordFromHook(stdin, time.Now()); err != nil {
		saveLastError(&renderError{err: err})
	}
}

func recordFromHook(stdin io.Reader, now time.Time) error {
	data, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		return err
	}
	var in hookInput
	_ = json.Unmarshal(data, &in) // a hook without input still sweeps
	ctx, cancel := context.WithTimeout(context.Background(), hookBudget)
	defer cancel()

	var entries []ledger.Entry
	if in.TranscriptPath != "" {
		if e, ok := recordSession(ctx, in.TranscriptPath, in.SessionID, in.Cwd, now); ok {
			entries = append(entries, e)
		}
	}
	swept, err := sweep(ctx, in.TranscriptPath, now)
	entries = append(entries, swept...)
	if aerr := ledger.Append(ledgerDir(), entries...); aerr != nil {
		return aerr
	}
	return err
}

// ledgerDir is where the cost ledger lives.
func ledgerDir() string { return filepath.Join(config.Dir(), ledger.DirName) }

// projectsDir holds Claude Code's transcripts, one folder per project.
func projectsDir() string { return filepath.Join(config.Dir(), "projects") }

// recordSession scans a transcript, through the same cache the status line uses, into a
// ledger entry. A session without responses, or one the budget ran out on, is not
// recorded: a partial total would stand until the transcript changed again.
func recordSession(ctx context.Context, path, session, cwdHint string, now time.Time) (ledger.Entry, bool) {
	act := transcript.ScanCached(ctx, path, transcriptCache())
	if ctx.Err() != nil || len(act.Cost) == 0 {
		return ledger.Entry{}, false
	}
	if session == "" {
		session = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	cwd := nativePath(act.Cwd)
	if cwd == "" {
		cwd = nativePath(cwdHint)
	}
	repo := cwd
	if cwd != "" {
		repo = gitinfo.Root(cwd)
	}
	return ledger.Entry{
		Session: session, Repo: repo, Cwd: cwd,
		Start: act.Start, End: act.Last, Written: now,
		USD: act.Cost.USD(), Unpriced: act.Cost.Unpriced(),
		Prices: cost.TableVersion, Models: act.Cost,
	}, true
}

// nativePath turns the Git Bash spelling of a Windows path (/c/Users/me) that Claude Code
// sometimes records into the one Windows and git understand (C:\Users\me).
func nativePath(p string) string {
	if runtime.GOOS != "windows" {
		return p
	}
	if w, ok := fromMSYS(p); ok {
		return w
	}
	return p
}

// fromMSYS converts /c/Users/me to C:\Users\me, and reports false for anything else.
func fromMSYS(p string) (string, bool) {
	if len(p) < 2 || p[0] != '/' || !isLetter(p[1]) || (len(p) > 2 && p[2] != '/') {
		return "", false
	}
	return strings.ToUpper(p[1:2]) + `:\` + strings.ReplaceAll(strings.TrimPrefix(p[2:], "/"), "/", `\`), true
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// sweep records the recent sessions the ledger is missing or has an older total for,
// newest first, until ctx ends. skip is the transcript the hook already recorded.
func sweep(ctx context.Context, skip string, now time.Time) ([]ledger.Entry, error) {
	known, _, err := ledger.Read(ledgerDir())
	if err != nil {
		return nil, err
	}
	written := map[string]time.Time{}
	for _, e := range known {
		written[e.Session] = e.Written
	}
	type candidate struct {
		path, session string
		mod           time.Time
	}
	files, _ := filepath.Glob(filepath.Join(projectsDir(), "*", "*.jsonl"))
	var todo []candidate
	for _, f := range files {
		name := filepath.Base(f)
		// Older Claude Code versions wrote subagents next to the sessions as agent-*.jsonl.
		if strings.HasPrefix(name, "agent-") || (skip != "" && sameFile(f, skip)) {
			continue
		}
		fi, err := os.Stat(f)
		if err != nil || now.Sub(fi.ModTime()) > sweepAge {
			continue
		}
		session := strings.TrimSuffix(name, ".jsonl")
		if w, ok := written[session]; ok && !fi.ModTime().After(w) {
			continue
		}
		todo = append(todo, candidate{f, session, fi.ModTime()})
	}
	slices.SortFunc(todo, func(a, b candidate) int { return b.mod.Compare(a.mod) })
	var entries []ledger.Entry
	for _, c := range todo {
		if ctx.Err() != nil {
			break
		}
		if e, ok := recordSession(ctx, c.path, c.session, "", now); ok {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

// sameFile reports whether two paths name the same file, whatever their spelling.
func sameFile(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}
