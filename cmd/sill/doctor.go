package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/LucasAntunesdeAlmeida/sill/internal/config"
	"github.com/LucasAntunesdeAlmeida/sill/internal/cost"
	"github.com/LucasAntunesdeAlmeida/sill/internal/gitinfo"
	"github.com/LucasAntunesdeAlmeida/sill/internal/install"
	"github.com/LucasAntunesdeAlmeida/sill/internal/ledger"
	"github.com/LucasAntunesdeAlmeida/sill/internal/render"
	"github.com/LucasAntunesdeAlmeida/sill/internal/term"
	"github.com/LucasAntunesdeAlmeida/sill/internal/transcript"
)

// checks prints one line per finding: ok, warn or FAIL, then details indented below.
type checks struct {
	w      io.Writer
	failed bool
}

func (c *checks) ok(format string, a ...any)   { c.line("ok  ", format, a...) }
func (c *checks) warn(format string, a ...any) { c.line("warn", format, a...) }
func (c *checks) fail(format string, a ...any) {
	c.failed = true
	c.line("FAIL", format, a...)
}
func (c *checks) detail(format string, a ...any) { fmt.Fprintf(c.w, "      "+format+"\n", a...) }
func (c *checks) line(level, format string, a ...any) {
	fmt.Fprintf(c.w, "%s  %s\n", level, fmt.Sprintf(format, a...))
}

// doctor checks the installation and shows what a render sees, since a render itself has
// nowhere to explain a problem. It fails only when the status line cannot work.
func doctor(stdout io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	fmt.Fprintf(stdout, "sill %s at %s\n\n", versionString(), filepath.ToSlash(exe))
	c := &checks{w: stdout}
	s := checkSettings(c)
	checkStatusLine(c, exe)
	checkHook(c, exe)
	checkGit(c, s)
	checkTerminal(c, s)
	checkTranscript(c)
	checkLedger(c)
	checkLastError(c)
	if c.failed {
		return errors.New("the status line will not work until the FAIL lines are fixed")
	}
	return nil
}

func checkStatusLine(c *checks, exe string) {
	settings := filepath.Join(config.Dir(), "settings.json")
	cmd, err := install.Current(config.Dir())
	switch {
	case err != nil:
		c.fail("%v", err)
		return
	case cmd == "":
		c.fail("%s has no statusLine; run `sill install`", settings)
		return
	case !install.IsSill(cmd):
		c.warn("the statusLine runs something else: %s", cmd)
		return
	}
	target := install.CommandPath(cmd)
	if !strings.ContainsAny(target, `/\`) {
		found, err := exec.LookPath(target)
		if err != nil {
			c.fail("the statusLine runs %q, which is not on PATH; run `sill install`", target)
			return
		}
		target = found
	}
	ti, err := os.Stat(target)
	if err != nil {
		c.fail("the statusLine runs %s, which does not exist; run `sill install`", target)
		return
	}
	if ei, err := os.Stat(exe); err == nil && os.SameFile(ti, ei) {
		c.ok("settings.json runs this binary")
		return
	}
	c.warn("settings.json runs %s, not this binary", target)
	c.detail("run `sill install` from the binary you want Claude Code to use")
}

// checkHook looks for the SessionEnd hook that records session costs. Without it the
// ledger only grows when `sill cost` runs, and misses what Claude Code deleted meanwhile.
func checkHook(c *checks, exe string) {
	hooks, err := install.HookCommands(config.Dir())
	switch {
	case err != nil:
		c.warn("cannot read the hooks in settings.json: %v", err)
		return
	case len(hooks) == 0:
		c.warn("no %s hook records session costs; run `sill install` to add it", install.HookEvent)
		return
	}
	for _, h := range hooks {
		target := install.CommandPath(h)
		ti, err := os.Stat(target)
		if err != nil {
			c.warn("the %s hook runs %s, which does not exist; run `sill install`", install.HookEvent, target)
			continue
		}
		if ei, err := os.Stat(exe); err == nil && os.SameFile(ti, ei) {
			c.ok("a %s hook records session costs", install.HookEvent)
		} else {
			c.warn("the %s hook runs %s, not this binary", install.HookEvent, target)
		}
	}
}

// checkLedger reports the cost ledger and the models it could not price.
func checkLedger(c *checks) {
	entries, bad, err := ledger.Read(ledgerDir())
	if err != nil {
		c.warn("cost ledger: %v", err)
		return
	}
	if len(entries) == 0 {
		c.ok("no session costs recorded yet")
		return
	}
	var size int64
	files, _ := filepath.Glob(filepath.Join(ledgerDir(), "*.jsonl"))
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil {
			size += fi.Size()
		}
	}
	total := cost.Totals{}
	for _, e := range entries {
		total.Merge(e.Models)
	}
	c.ok("cost ledger: %d session(s), %s at list prices, %d file(s), %.1f KB",
		len(entries), render.Dollars(total.USD()), len(files), float64(size)/1024)
	c.detail("%s", filepath.ToSlash(ledgerDir()))
	if bad > 0 {
		c.warn("cost ledger: %d line(s) could not be read and are skipped", bad)
	}
	warnUnpriced(c, total)
}

// warnUnpriced names the models whose tokens had no price, so their cost is missing.
func warnUnpriced(c *checks, t cost.Totals) {
	for _, m := range t.Models() {
		if n := t[m].Unpriced; n > 0 {
			c.warn("no price for %s (%s tokens): costs that include it are a lower bound", render.Clean(m), render.Tokens(int(n)))
		}
	}
}

// checkSettings validates sill.json and returns the settings a render would use.
func checkSettings(c *checks) config.Settings {
	s, loadErr := config.Load()
	data, err := os.ReadFile(config.Path())
	switch {
	case errors.Is(err, os.ErrNotExist):
		c.ok("no %s, every option at its default", config.FileName)
		return s
	case err != nil:
		c.fail("%v", err)
		return s
	}
	problems, err := config.Check(data)
	if err != nil || loadErr != nil {
		c.warn("%s is not valid JSON, renders use the defaults: %v", config.Path(), err)
		return s
	}
	for _, p := range problems {
		c.warn("%s: %s", config.FileName, p)
	}
	if len(problems) == 0 {
		changed := 0
		for _, o := range config.Options {
			if !s.IsDefault(o.Name) {
				changed++
			}
		}
		c.ok("%s is valid, %d option(s) changed", config.FileName, changed)
	}
	return s
}

func checkGit(c *checks, s config.Settings) {
	_, gitErr := exec.LookPath("git")
	if gitErr != nil && s.On("dirty") {
		c.warn("git is not on PATH, so dirty cannot be shown")
	}
	cwd, err := os.Getwd()
	if err != nil || !(s.On("git") || s.On("worktree")) {
		return
	}
	start := time.Now()
	st, fast := gitinfo.ReadHead(cwd)
	took := time.Since(start)
	switch {
	case fast && st.Branch == "":
		c.ok("git: this folder is in no repository")
	case fast:
		c.ok("git: branch %s read from .git/HEAD in %s, no git process", st.Branch, ms(took))
	case gitErr != nil:
		c.warn("git: this repository needs git to read its branch, and git is not on PATH")
	default:
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()
		start = time.Now()
		st = gitinfo.Lookup(ctx, cwd, false)
		c.ok("git: branch %s from a git process in %s (reftable, GIT_DIR or an unusual HEAD)", st.Branch, ms(time.Since(start)))
	}
}

func checkTerminal(c *checks, s config.Settings) {
	if w := s.Int("width"); w > 0 {
		c.ok("width fixed at %d columns by the width option", w)
	} else if w := term.Width(); w > 0 {
		c.ok("terminal width %d columns", w)
	} else {
		c.warn("terminal width unknown, lines are not fitted; set one with `sill set width 120`")
	}
	if os.Getenv("NO_COLOR") != "" && s.On("color") {
		c.ok("NO_COLOR is set, so colors are off")
	}
}

// checkTranscript inspects the most recently written transcript: what the scan finds, how
// long a render spends on it, and whether the format still matches the scan.
func checkTranscript(c *checks) {
	path := newestTranscript(filepath.Join(config.Dir(), "projects"))
	if path == "" {
		c.ok("no session transcripts yet")
		return
	}
	start := time.Now()
	rep, err := transcript.Inspect(path)
	full := time.Since(start)
	if err != nil {
		c.warn("transcript %s: %v", path, err)
		return
	}
	size := int64(0)
	if fi, err := os.Stat(path); err == nil {
		size = fi.Size()
	}
	c.ok("latest transcript: %d records, %d agent(s) running, %d compaction(s), %d response(s)",
		rep.Records, rep.Activity.Agents, rep.Activity.Compactions, rep.Responses)
	c.detail("%s (%.1f MB)", filepath.ToSlash(path), float64(size)/(1<<20))

	cache, err := os.MkdirTemp("", "sill-doctor")
	if err == nil {
		defer os.RemoveAll(cache)
		transcript.ScanCached(context.Background(), path, cache)
		start = time.Now()
		transcript.ScanCached(context.Background(), path, cache)
		c.detail("full scan %s, a render with nothing new %s", ms(full), ms(time.Since(start)))
	}
	for _, d := range rep.Drift() {
		c.warn("transcript: %s", d)
	}
	if len(rep.Drift()) > 0 {
		c.detail("the transcript format may have changed; agents, compactions and costs can be undercounted")
		c.detail("please report it with your Claude Code version")
	}
}

// newestTranscript is the most recently modified session file under projects/*/.
func newestTranscript(projects string) string {
	files, _ := filepath.Glob(filepath.Join(projects, "*", "*.jsonl"))
	var newest string
	var when time.Time
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil && fi.ModTime().After(when) {
			newest, when = f, fi.ModTime()
		}
	}
	return newest
}

func checkLastError(c *checks) {
	dir := cacheDir()
	if dir == "" {
		return
	}
	path := filepath.Join(dir, lastErrorFile)
	data, err := os.ReadFile(path)
	if err != nil {
		c.ok("no failed render recorded")
		return
	}
	fields := map[string]string{}
	for line := range strings.Lines(string(data)) {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ": "); ok && fields[k] == "" {
			fields[k] = v
		}
	}
	c.warn("a render failed at %s: %s", fields["time"], fields["error"])
	c.detail("details in %s; delete it once dealt with", filepath.ToSlash(path))
}

// ms formats a duration in milliseconds with one decimal.
func ms(d time.Duration) string {
	return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000)
}
