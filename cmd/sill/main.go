// Command sill renders a status line for the Claude Code CLI from the JSON it pipes on
// stdin, and manages its own settings and installation.
package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/LucasAntunesdeAlmeida/sill/internal/config"
	"github.com/LucasAntunesdeAlmeida/sill/internal/gitinfo"
	"github.com/LucasAntunesdeAlmeida/sill/internal/install"
	"github.com/LucasAntunesdeAlmeida/sill/internal/payload"
	"github.com/LucasAntunesdeAlmeida/sill/internal/render"
	"github.com/LucasAntunesdeAlmeida/sill/internal/term"
	"github.com/LucasAntunesdeAlmeida/sill/internal/transcript"
)

// version is set by the release build with -ldflags "-X main.version=v1.2.3".
var version = "dev"

// versionString is the ldflags version, or the module version stamped by
// "go install ...@v1.2.3" when the binary was not built through the Makefile.
func versionString() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

const usage = `sill: a status line for Claude Code.

Usage:
  sill                    render (Claude Code pipes its JSON on stdin)
  sill install            point settings.json at this binary
  sill uninstall          remove the statusLine entry again
  sill settings           list options and current values
  sill set <key> <value>  change options, e.g. sill set layout full cache on
  sill demo               render a sample payload with the current settings
  sill version            print the version

Inside Claude Code, run any of these without a model turn:
  ! sill set layout full
`

//go:embed demo.json
var demoPayload []byte

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "sill:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return renderStdin(stdin, stdout)
	}
	switch args[0] {
	case "install":
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return install.Run(config.Dir(), exe, stdout)
	case "uninstall":
		return install.Uninstall(config.Dir(), stdout)
	case "settings":
		return settings(stdout)
	case "set":
		return set(args[1:], stdout)
	case "demo":
		return demo(stdout)
	case "version", "-v", "--version":
		_, err := fmt.Fprintln(stdout, "sill", versionString())
		return err
	case "help", "-h", "--help":
		_, err := io.WriteString(stdout, usage)
		return err
	}
	return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
}

func renderStdin(stdin io.Reader, stdout io.Writer) error {
	if f, ok := stdin.(*os.File); ok {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			_, err := io.WriteString(stdout, usage)
			return err
		}
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	p, err := payload.Parse(data)
	if err != nil {
		return fmt.Errorf("payload: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	_, err = io.WriteString(stdout, render.Render(p, gather(ctx, p)))
	return err
}

// budget is how long a render may spend on git and the transcript together. Past it, git
// is not waited for and the transcript scan resumes on the next render, so a hung network
// drive or a huge first scan costs one slow line instead of a frozen one.
const budget = time.Second

// cacheDir holds sill's own state: $SILL_CACHE_DIR, or sill in the user cache directory
// (~/.cache/sill, ~/Library/Caches/sill, %LocalAppData%\sill). Empty when there is none.
func cacheDir() string {
	if d := os.Getenv("SILL_CACHE_DIR"); d != "" {
		return d
	}
	d, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "sill")
}

// transcriptCache is where the incremental transcript scans keep their state.
func transcriptCache() string {
	if d := cacheDir(); d != "" {
		return filepath.Join(d, "transcripts")
	}
	return ""
}

// gather loads settings and collects only what the enabled segments need.
func gather(ctx context.Context, p *payload.Payload) render.State {
	s, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sill:", err) // keep rendering with defaults
	}
	home, _ := os.UserHomeDir()
	st := render.State{
		Settings: s,
		Layout:   s.Layout(),
		Home:     home,
		Width:    width(s),
		Color:    colorEnabled(s),
	}
	if s.On("git") || s.On("worktree") {
		st.Git = gitinfo.Lookup(ctx, p.CurrentDir(), s.On("dirty"))
	}
	switch {
	case s.On("agents") || s.On("compactions"):
		st.Activity = transcript.ScanCached(ctx, p.TranscriptPath, transcriptCache())
	case s.On("duration"):
		st.Activity.Start = transcript.Start(p.TranscriptPath)
	}
	return st
}

// width is the configured width, or the detected one.
func width(s config.Settings) int {
	if w := s.Int("width"); w > 0 {
		return w
	}
	return term.Width()
}

// colorEnabled honors the color option and the NO_COLOR convention (https://no-color.org).
func colorEnabled(s config.Settings) bool {
	return s.On("color") && os.Getenv("NO_COLOR") == ""
}

func settings(stdout io.Writer) error {
	s, err := config.Load()
	if err != nil {
		return err
	}
	if _, err := io.WriteString(stdout, s.Describe()); err != nil {
		return err
	}
	if w := term.Width(); w > 0 {
		_, err = fmt.Fprintf(stdout, "Detected terminal width: %d columns\n", w)
	} else {
		_, err = fmt.Fprintln(stdout, "Detected terminal width: unknown (set width to fit lines)")
	}
	return err
}

func set(args []string, stdout io.Writer) error {
	if len(args) == 0 || len(args)%2 != 0 {
		return fmt.Errorf("set takes key value pairs, e.g. sill set layout full cache on")
	}
	s, err := config.Load()
	if err != nil {
		return err
	}
	for i := 0; i < len(args); i += 2 {
		if err := s.Set(args[i], args[i+1]); err != nil {
			return err
		}
	}
	if err := s.Save(); err != nil {
		return err
	}
	for i := 0; i < len(args); i += 2 {
		fmt.Fprintf(stdout, "%s = %s\n", args[i], s.Get(args[i]))
	}
	if s.Get("layout") == "custom" && len(s.Lines) == 0 {
		fmt.Fprintf(stdout, "layout is custom but %s has no \"lines\", showing compact until it does\n", config.Path())
	}
	return nil
}

// demo renders the embedded sample payload so a settings change can be previewed without
// Claude Code. Times in the sample are made relative to now and the path to home.
func demo(stdout io.Writer) error {
	s, err := config.Load()
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	doc := strings.ReplaceAll(string(demoPayload), "$HOME", strings.ReplaceAll(home, `\`, "/"))
	p, err := payload.Parse([]byte(doc))
	if err != nil {
		return fmt.Errorf("demo payload: %w", err)
	}
	now := time.Now()
	in := func(d time.Duration) float64 { return float64(now.Add(d).Unix()) }
	p.RateLimits.FiveHour.ResetsAt = in(80 * time.Minute)
	p.RateLimits.SevenDay.ResetsAt = in(53 * time.Hour)
	expires := in(42 * time.Minute)
	p.PromptCache.ExpiresAt = &expires

	st := render.State{
		Settings: s,
		Layout:   s.Layout(),
		Home:     home,
		Width:    width(s),
		Color:    colorEnabled(s),
		Git:      gitinfo.State{Branch: "feature/billing", Status: "MERGING", Dirty: true},
		Activity: transcript.Activity{Agents: 2, Compactions: 1, Start: now.Add(-135 * time.Minute)},
	}
	_, err = fmt.Fprintln(stdout, render.Render(p, st))
	return err
}
