// Package render turns a payload and the gathered state into the status line text.
//
// Design rules: the first segment of a line never clips, so budgets lead their line; the
// path is the one elastic segment; the model anchors the right. Segments with nothing to
// show disappear, nothing prints a zero. Output is plain ASCII with ANSI colors: labels
// and separators are gray so the line reads as content with quiet structure, and only
// budgets change color. When the terminal width is known, a line that would overflow is
// degraded in a fixed order instead of wrapping.
package render

import (
	"fmt"
	"math"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/LucasAntunesdeAlmeida/sill/internal/config"
	"github.com/LucasAntunesdeAlmeida/sill/internal/gitinfo"
	"github.com/LucasAntunesdeAlmeida/sill/internal/payload"
	"github.com/LucasAntunesdeAlmeida/sill/internal/transcript"
)

// ANSI colors.
const (
	Red    = "\x1b[31m"
	Green  = "\x1b[32m"
	Yellow = "\x1b[33m"
	Gray   = "\x1b[90m"
	Reset  = "\x1b[0m"
)

// Thresholds, in percent, at which budgets turn yellow and red.
const (
	ctxWarn, ctxCrit     = 60, 80
	limitWarn, limitCrit = 70, 90
)

// Now is the clock; tests replace it.
var Now = time.Now

// State is everything a render needs besides the payload.
type State struct {
	Settings config.Settings
	Layout   config.Layout
	Git      gitinfo.State
	Activity transcript.Activity
	Home     string // the user's home directory, shown as ~
	Width    int    // terminal columns, 0 when unknown (no fitting)
	Color    bool   // emit ANSI colors
}

// Render produces the status line, one string per layout line joined with newlines.
func Render(p *payload.Payload, st State) string {
	r := renderer{p: p, st: st}
	if st.Color {
		r.pal = colors
	}
	var out []string
	for _, spec := range st.Layout.Lines {
		if line := r.fit(spec); line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

type palette struct{ red, green, yellow, gray, reset string }

var colors = palette{Red, Green, Yellow, Gray, Reset}

type renderer struct {
	p   *payload.Payload
	st  State
	pal palette

	// Degradation state, reset for every line.
	pathShrink int             // extra levels of path shortening
	noReset    bool            // limits without their reset time
	dropped    map[string]bool // segments removed to make room
}

func (r *renderer) dim(s string) string { return r.pal.gray + s + r.pal.reset }

// degradeSteps is the order in which a line gives up detail when it does not fit.
var degradeSteps = []func(*renderer){
	func(r *renderer) { r.pathShrink = 1 },
	func(r *renderer) { r.pathShrink = 2 },
	func(r *renderer) { r.noReset = true },
	func(r *renderer) { r.dropped["version"] = true },
	func(r *renderer) { r.dropped["duration"] = true },
	func(r *renderer) { r.dropped["session"] = true },
	func(r *renderer) { r.dropped["compactions"] = true },
	func(r *renderer) { r.dropped["agents"] = true },
	func(r *renderer) { r.dropped["cache"] = true },
	func(r *renderer) { r.dropped["effort"] = true },
	func(r *renderer) { r.dropped["worktree"] = true },
	func(r *renderer) { r.dropped["pr"] = true },
	func(r *renderer) { r.dropped["git"] = true },
	func(r *renderer) { r.dropped["path"] = true },
}

// fit renders a line and, when the width is known, degrades it until it fits. A line
// that still overflows after every step is cut at the right edge.
func (r *renderer) fit(spec string) string {
	r.pathShrink, r.noReset, r.dropped = 0, false, map[string]bool{}
	text := r.line(spec)
	if r.st.Width <= 0 {
		return text
	}
	limit := r.st.Width - 1 // the last column would wrap on terminals with auto-margins
	for i := 0; visibleWidth(text) > limit; i++ {
		if i >= len(degradeSteps) {
			return truncate(text, limit) + r.pal.reset
		}
		degradeSteps[i](r)
		text = r.line(spec)
	}
	return text
}

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func visibleWidth(s string) int {
	return utf8.RuneCountInString(ansiSeq.ReplaceAllString(s, ""))
}

// truncate keeps the first n visible characters, copying escape sequences through.
func truncate(s string, n int) string {
	var b strings.Builder
	seen := 0
	for i := 0; i < len(s); {
		if loc := ansiSeq.FindStringIndex(s[i:]); loc != nil && loc[0] == 0 {
			b.WriteString(s[i : i+loc[1]])
			i += loc[1]
			continue
		}
		if seen >= n {
			break
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		b.WriteString(s[i : i+size])
		i += size
		seen++
	}
	return b.String()
}

// Separators between segments, weakest first. An empty segment drops the separator
// before it, except a bar, which stays so groups keep their boundaries.
const (
	sepSpace = "  "
	sepSlash = " / "
	sepBar   = " | "
)

func sepRank(sep string) int {
	switch sep {
	case sepBar:
		return 2
	case sepSlash:
		return 1
	}
	return 0
}

func (r *renderer) line(spec string) string {
	var b strings.Builder
	pending := sepSpace
	for tok := range strings.FieldsSeq(spec) {
		switch tok {
		case "|":
			pending = sepBar
		case "/":
			if sepRank(pending) < sepRank(sepSlash) {
				pending = sepSlash
			}
		default:
			text := r.segment(tok)
			if text == "" {
				if pending != sepBar {
					pending = sepSpace
				}
				continue
			}
			if b.Len() > 0 {
				if pending == sepSpace {
					b.WriteString(pending)
				} else {
					b.WriteString(r.dim(pending))
				}
			}
			b.WriteString(text)
			pending = sepSpace
		}
	}
	return b.String()
}

// segment renders one named segment, or "" when it is off or has nothing to show.
func (r *renderer) segment(name string) string {
	if o := config.Find(name); o == nil || o.Kind != config.Bool || !r.st.Settings.On(name) || r.dropped[name] {
		return ""
	}
	p := r.p
	switch name {
	case "ctx":
		v := p.ContextPercent()
		if v == nil {
			return ""
		}
		s := r.dim("ctx ") + r.pct(*v, ctxWarn, ctxCrit)
		if cw := p.ContextWindow; r.st.Layout.Wide && cw.ContextWindowSize > 0 {
			s += r.dim(fmt.Sprintf(" (%s/%s)", tokens(cw.TotalInputTokens), tokens(cw.ContextWindowSize)))
		}
		return s
	case "limits":
		return r.limits()
	case "cache":
		pc := p.PromptCache
		if pc == nil || !pc.CachingObserved {
			return ""
		}
		if pc.Warm && pc.ExpiresAt != nil {
			return r.dim("cache ") + until(*pc.ExpiresAt)
		}
		return r.dim("cache ") + r.pal.yellow + "cold" + r.pal.reset
	case "agents":
		if n := r.st.Activity.Agents; n > 0 {
			return r.dim("agents ") + strconv.Itoa(n)
		}
	case "compactions":
		if n := r.st.Activity.Compactions; n > 0 {
			return r.dim("compact ") + strconv.Itoa(n)
		}
	case "duration":
		if start := r.st.Activity.Start; !start.IsZero() {
			return r.dim("up ") + span(int64(Now().Sub(start).Seconds()))
		}
	case "path":
		cwd := p.CurrentDir()
		if cwd == "" {
			return ""
		}
		level := r.pathShrink
		if !r.st.Layout.Wide {
			level++
		}
		return Clean(DisplayPath(cwd, r.st.Home, level))
	case "git":
		g := r.st.Git
		if g.Branch == "" {
			return ""
		}
		s := Clean(g.Branch)
		if g.Dirty && r.st.Settings.On("dirty") {
			s += "*"
		}
		if g.Status != "" {
			s += " " + r.pal.red + g.Status + r.pal.reset
		}
		return s
	case "worktree":
		wt := p.WorktreeName()
		if wt == "" {
			return ""
		}
		if wt == r.st.Git.Branch {
			return r.dim("wt")
		}
		return r.dim("wt:" + Clean(wt))
	case "pr":
		if p.PR.Number == 0 {
			return ""
		}
		s := "#" + strconv.Itoa(p.PR.Number)
		if p.PR.Kind == "mr" {
			s = "!" + strconv.Itoa(p.PR.Number)
		}
		switch p.PR.ReviewState {
		case "approved":
			s += " " + r.pal.green + "+" + r.pal.reset
		case "changes_requested":
			s += " " + r.pal.red + "x" + r.pal.reset
		case "draft":
			s = r.dim(s + " draft")
		}
		return s
	case "model":
		return Clean(p.ModelName())
	case "effort":
		return Clean(p.Effort.Level)
	case "session":
		return Clean(p.SessionName)
	case "version":
		if p.Version != "" {
			return r.dim("v" + Clean(p.Version))
		}
	}
	return ""
}

// Clean makes text from outside sill safe to print. Control characters could clear the
// screen, retitle the window or split the line, and bidi overrides could reorder what is
// shown, so each becomes "?"; invalid UTF-8 does too. A session or folder name is data,
// never terminal commands.
func Clean(s string) string {
	s = strings.ToValidUTF8(s, "?")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Bidi_Control, unicode.Zl, unicode.Zp) {
			return '?'
		}
		return r
	}, s)
}

// limits renders the rate limit windows. The reset time shows from yellow on, or always
// in a wide layout, unless fitting removed it.
func (r *renderer) limits() string {
	rl := r.p.RateLimits
	windows := []struct {
		label string
		win   *payload.LimitWindow
	}{{"5h", rl.FiveHour}, {"7d", rl.SevenDay}, {"spend", rl.SpendLimit}}

	var parts []string
	for _, w := range windows {
		if w.win == nil || w.win.UsedPercentage == nil {
			continue
		}
		used := *w.win.UsedPercentage
		s := r.dim(w.label+" ") + r.pct(used, limitWarn, limitCrit)
		if w.win.ResetsAt > 0 && !r.noReset && (r.st.Layout.Wide || math.Round(used) >= limitWarn) {
			s += r.dim(" (" + resetLabel(w.win.ResetsAt, r.st.Settings.Get("reset")) + ")")
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, sepSpace)
}

// pct formats a percentage, colored from warn (yellow) and crit (red). Rounding is half
// away from zero so 69.5 already counts as 70.
func (r *renderer) pct(v float64, warn, crit int) string {
	n := int(math.Round(v))
	s := strconv.Itoa(n) + "%"
	switch {
	case n >= crit:
		return r.pal.red + s + r.pal.reset
	case n >= warn:
		return r.pal.yellow + s + r.pal.reset
	}
	return s
}

// span formats a number of seconds: 35m, 1h05, 2d5h. Under a minute is 0m.
func span(secs int64) string {
	if secs < 0 {
		secs = 0
	}
	m := (secs + 59) / 60
	if m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	h, m := m/60, m%60
	if h < 24 {
		return fmt.Sprintf("%dh%02d", h, m)
	}
	return fmt.Sprintf("%dd%dh", h/24, h%24)
}

// until formats the time from now until a unix epoch, or "now" once it has passed.
func until(epoch float64) string {
	secs := int64(epoch) - Now().Unix()
	if secs <= 0 {
		return "now"
	}
	return span(secs)
}

// at formats a unix epoch as local wall-clock time, with the weekday once a day away.
func at(epoch float64) string {
	t := time.Unix(int64(epoch), 0).In(Now().Location())
	if t.Sub(Now()) >= 24*time.Hour {
		return t.Format("Mon 15:04")
	}
	return t.Format("15:04")
}

func resetLabel(epoch float64, mode string) string {
	switch mode {
	case "absolute":
		return "@ " + at(epoch)
	case "both":
		return until(epoch) + " @ " + at(epoch)
	}
	return until(epoch)
}

// tokens formats a token count: 850, 9.5k, 122k, 1m.
func tokens(n int) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "m"
	case n >= 10_000:
		return strconv.Itoa(int(math.Round(float64(n)/1000))) + "k"
	case n >= 1000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1000), ".0") + "k"
	}
	return strconv.Itoa(n)
}

// DisplayPath shortens the home directory to ~ and then, by level, the path itself:
// 0 keeps it whole, 1 keeps the anchor and the last two folders (~/.../utils/sill),
// 2 the anchor and the last folder (~/.../sill), 3 only the last folder (sill).
// Windows paths are compared case-insensitively and shown with forward slashes.
func DisplayPath(cwd, home string, level int) string {
	cwd = strings.ReplaceAll(cwd, `\`, "/")
	home = strings.TrimSuffix(strings.ReplaceAll(home, `\`, "/"), "/")
	p := cwd
	if home != "" && hasPathPrefix(cwd, home) {
		p = "~" + cwd[len(home):]
	}
	if level <= 0 {
		return p
	}
	var parts []string
	for seg := range strings.SplitSeq(p, "/") {
		if seg != "" {
			parts = append(parts, seg)
		}
	}
	if len(parts) == 0 {
		return p
	}
	if level >= 3 {
		return parts[len(parts)-1]
	}
	keep := 3 - level // trailing folders to keep after the anchor
	if len(parts) <= keep+2 {
		return p
	}
	anchor := parts[0]
	if strings.HasPrefix(p, "/") {
		anchor = "/" + anchor
	}
	return anchor + "/.../" + strings.Join(parts[len(parts)-keep:], "/")
}

func hasPathPrefix(path, prefix string) bool {
	if len(path) < len(prefix) {
		return false
	}
	head, rest := path[:len(prefix)], path[len(prefix):]
	if rest != "" && rest[0] != '/' {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(head, prefix)
	}
	return head == prefix
}
