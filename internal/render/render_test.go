package render

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/LucasAntunesdeAlmeida/sill/internal/config"
	"github.com/LucasAntunesdeAlmeida/sill/internal/cost"
	"github.com/LucasAntunesdeAlmeida/sill/internal/gitinfo"
	"github.com/LucasAntunesdeAlmeida/sill/internal/payload"
	"github.com/LucasAntunesdeAlmeida/sill/internal/transcript"
)

func plain(s string) string { return ansiSeq.ReplaceAllString(s, "") }

// fixedNow pins the clock to a Friday noon in UTC and returns it.
func fixedNow(t *testing.T) time.Time {
	t.Helper()
	base := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	old := Now
	Now = func() time.Time { return base }
	t.Cleanup(func() { Now = old })
	return base
}

// samplePayload has every segment populated, with reset times relative to base:
// 5h resets in 80 minutes, 7d in 50 hours, the cache goes cold in 80 minutes.
func samplePayload(t testing.TB, base time.Time) *payload.Payload {
	t.Helper()
	epoch := func(d time.Duration) int64 { return base.Add(d).Unix() }
	doc := fmt.Sprintf(`{
		"model": {"display_name": "Fable 5.1"},
		"workspace": {"current_dir": "/home/me/source/repos/utils/sill", "repo": {"host": "github.com", "owner": "acme", "name": "storefront"}},
		"context_window": {"used_percentage": 61.2, "total_input_tokens": 122400, "context_window_size": 200000},
		"effort": {"level": "high"},
		"session_name": "billing-fix",
		"version": "2.1.282",
		"rate_limits": {
			"five_hour": {"used_percentage": 94, "resets_at": %d},
			"seven_day": {"used_percentage": 72, "resets_at": %d}
		},
		"prompt_cache": {"warm": true, "caching_observed": true, "expires_at": %d},
		"worktree": {"name": "feature/billing"},
		"pr": {"number": 42, "review_state": "approved"}
	}`, epoch(80*time.Minute), epoch(50*time.Hour), epoch(80*time.Minute))
	p, err := payload.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// sampleState pairs the sample payload with a merging branch, two agents, one compaction
// and a session that started 2h15 ago.
func sampleState(s config.Settings, base time.Time) State {
	if s.IsDefault("duration") {
		_ = s.Set("duration", "on") // off by default; the sample shows it
	}
	return State{
		Settings: s,
		Layout:   s.Layout(),
		Home:     "/home/me",
		Color:    true,
		Git:      gitinfo.State{Branch: "feature/billing", Status: "MERGING"},
		Activity: transcript.Activity{Agents: 2, Compactions: 1, Start: base.Add(-135 * time.Minute)},
	}
}

const compactWant = "ctx 61%  5h 94% (1h20)  7d 72% (2d2h)  agents 2  compact 1 | ~/.../utils/sill  feature/billing MERGING  wt  #42 + | Fable 5.1 / high / billing-fix  up 2h15"

func TestRenderCompact(t *testing.T) {
	base := fixedNow(t)
	got := plain(Render(samplePayload(t, base), sampleState(config.New(), base)))
	if got != compactWant {
		t.Errorf("\n got %q\nwant %q", got, compactWant)
	}
	if strings.Contains(got, "\n") {
		t.Error("compact layout must be one line")
	}
	for _, r := range got {
		if r > 127 {
			t.Fatalf("non-ASCII output %q", r)
		}
	}
}

func TestRenderFull(t *testing.T) {
	base := fixedNow(t)
	s := config.New()
	for k, v := range map[string]string{"layout": "full", "cache": "on", "version": "on", "reset": "both"} {
		if err := s.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	st := sampleState(s, base)
	st.Git = gitinfo.State{Branch: "feature/billing"}

	got := strings.Split(plain(Render(samplePayload(t, base), st)), "\n")
	want := []string{
		"~/source/repos/utils/sill  feature/billing  wt  #42 +",
		"ctx 61% (122k/200k)  5h 94% (1h20 @ 13:20)  7d 72% (2d2h @ Sun 14:00)  cache 1h20",
		"Fable 5.1 / high / billing-fix  up 2h15  agents 2  compact 1  v2.1.282",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d\n got %q\nwant %q", i+1, got[i], want[i])
		}
	}
}

func TestRenderCustomLayout(t *testing.T) {
	base := fixedNow(t)
	s := config.New()
	_ = s.Set("layout", "custom")
	s.Lines = []string{"model | path", "ctx"}
	st := sampleState(s, base)
	// Two lines make the layout wide, so the path is not shortened.
	if got := plain(Render(samplePayload(t, base), st)); got != "Fable 5.1 | ~/source/repos/utils/sill\nctx 61% (122k/200k)" {
		t.Errorf("got %q", got)
	}
}

func TestRenderEmptyPayload(t *testing.T) {
	p, _ := payload.Parse([]byte(`{}`))
	st := State{Settings: config.New(), Layout: config.New().Layout(), Home: "/home/me"}
	if got := Render(p, st); got != "" {
		t.Errorf("empty payload rendered %q", got)
	}
	p, _ = payload.Parse([]byte(`{"model":{"id":"claude-x"},"cwd":"/tmp/x"}`))
	if got := plain(Render(p, st)); got != "/tmp/x | claude-x" {
		t.Errorf("got %q", got)
	}
}

func TestRenderToggles(t *testing.T) {
	base := fixedNow(t)
	s := config.New()
	for _, k := range []string{"ctx", "limits", "git", "worktree", "pr", "effort", "session", "duration", "compactions"} {
		_ = s.Set(k, "off")
	}
	if got := plain(Render(samplePayload(t, base), sampleState(s, base))); got != "agents 2 | ~/.../utils/sill | Fable 5.1" {
		t.Errorf("got %q", got)
	}
}

func TestColorOff(t *testing.T) {
	base := fixedNow(t)
	st := sampleState(config.New(), base)
	st.Color = false
	got := Render(samplePayload(t, base), st)
	if strings.Contains(got, "\x1b") {
		t.Errorf("color off still emitted escapes: %q", got)
	}
	if got != compactWant {
		t.Errorf("\n got %q\nwant %q", got, compactWant)
	}
	st.Color = true
	if !strings.Contains(Render(samplePayload(t, base), st), Red+"94%"+Reset) {
		t.Error("color on should color the red limit")
	}
}

func TestDirty(t *testing.T) {
	base := fixedNow(t)
	s := config.New()
	st := sampleState(s, base)
	st.Git = gitinfo.State{Branch: "main", Dirty: true}
	if got := plain(Render(samplePayload(t, base), st)); !strings.Contains(got, "  main  ") {
		t.Errorf("dirty off should show a bare branch: %q", got)
	}
	_ = s.Set("dirty", "on")
	st.Settings = s
	if got := plain(Render(samplePayload(t, base), st)); !strings.Contains(got, "  main*  ") {
		t.Errorf("dirty on should mark the branch: %q", got)
	}
}

func TestFit(t *testing.T) {
	base := fixedNow(t)
	p := samplePayload(t, base)
	full := sampleState(config.New(), base)
	fullWidth := visibleWidth(Render(p, full))

	cases := []struct {
		width int
		want  string
	}{
		{fullWidth + 1, compactWant},
		{fullWidth, "ctx 61%  5h 94% (1h20)  7d 72% (2d2h)  agents 2  compact 1 | ~/.../sill  feature/billing MERGING  wt  #42 + | Fable 5.1 / high / billing-fix  up 2h15"},
		{fullWidth - 8, "ctx 61%  5h 94% (1h20)  7d 72% (2d2h)  agents 2  compact 1 | sill  feature/billing MERGING  wt  #42 + | Fable 5.1 / high / billing-fix  up 2h15"},
		{fullWidth - 20, "ctx 61%  5h 94%  7d 72%  agents 2  compact 1 | sill  feature/billing MERGING  wt  #42 + | Fable 5.1 / high / billing-fix  up 2h15"},
		{fullWidth - 28, "ctx 61%  5h 94%  7d 72%  agents 2  compact 1 | sill  feature/billing MERGING  wt  #42 + | Fable 5.1 / high / billing-fix"},
		{fullWidth - 40, "ctx 61%  5h 94%  7d 72%  agents 2  compact 1 | sill  feature/billing MERGING  wt  #42 + | Fable 5.1 / high"},
		{60, "ctx 61%  5h 94%  7d 72% | sill | Fable 5.1"},
		{40, "ctx 61%  5h 94%  7d 72% | Fable 5.1"},
		{30, "ctx 61%  5h 94%  7d 72% | Fab"},
	}
	for _, c := range cases {
		st := full
		st.Width = c.width
		got := plain(Render(p, st))
		if got != c.want {
			t.Errorf("width %d\n got %q\nwant %q", c.width, got, c.want)
		}
		if visibleWidth(got) > c.width-1 {
			t.Errorf("width %d: line is %d wide", c.width, visibleWidth(got))
		}
	}
}

func TestFitKeepsEscapesBalanced(t *testing.T) {
	base := fixedNow(t)
	st := sampleState(config.New(), base)
	st.Width = 30
	got := Render(samplePayload(t, base), st)
	if !strings.HasSuffix(got, Reset) {
		t.Errorf("truncated line should end with a reset: %q", got)
	}
	if visibleWidth(got) != 29 {
		t.Errorf("visible width = %d", visibleWidth(got))
	}
}

func TestRuneWidth(t *testing.T) {
	cases := map[rune]int{
		'a': 1, '~': 1, '\u00e9': 1, '\u0301': 0, '\u200b': 0, '\u200d': 0,
		'\u4e2d': 2, '\uac00': 2, '\u3042': 2, '\uff21': 2, '\U0001F600': 2, '\u2705': 2,
		'\u2b50': 2, '\u26a1': 2, '\u2600': 1, '\u2192': 1, '\u0416': 1, '\U00020000': 2,
	}
	for r, want := range cases {
		if got := runeWidth(r); got != want {
			t.Errorf("runeWidth(%U) = %d, want %d", r, got, want)
		}
	}
	// unicode.Is binary-searches the table, so it has to stay sorted and disjoint.
	last := rune(-1)
	for _, r := range wide.R16 {
		if rune(r.Lo) <= last || r.Hi < r.Lo || (r.Hi-r.Lo)%r.Stride != 0 {
			t.Errorf("bad range %04x-%04x/%d", r.Lo, r.Hi, r.Stride)
		}
		last = rune(r.Hi)
	}
	for _, r := range wide.R32 {
		if rune(r.Lo) <= last || r.Hi < r.Lo || (r.Hi-r.Lo)%r.Stride != 0 {
			t.Errorf("bad range %05x-%05x/%d", r.Lo, r.Hi, r.Stride)
		}
		last = rune(r.Hi)
	}
}

// A wide session name must count double, or the fitted line wraps anyway.
func TestFitWideText(t *testing.T) {
	p, err := payload.Parse([]byte(`{"model":{"display_name":"M"},"session_name":"\u4e2d\u6587\u4e2d\u6587\u4e2d\u6587\u4e2d\u6587\u4e2d\u6587"}`))
	if err != nil {
		t.Fatal(err)
	}
	s := config.New()
	for _, width := range []int{8, 15, 25} {
		got := Render(p, State{Settings: s, Layout: s.Layout(), Width: width})
		if w := visibleWidth(got); w > width-1 {
			t.Errorf("width %d: %q takes %d columns", width, got, w)
		}
	}
	if got := truncate("ab\u4e2d\u6587", 3); got != "ab" {
		t.Errorf("a wide character must not straddle the edge, got %q", got)
	}
	if got := visibleWidth(Red + "\u4e2da\u0301" + Reset); got != 3 {
		t.Errorf("visibleWidth = %d, want 3", got)
	}
}

func TestTruncate(t *testing.T) {
	in := Red + "abc" + Reset + "def"
	if got := truncate(in, 4); got != Red+"abc"+Reset+"d" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate(in, 10); got != in {
		t.Errorf("no-op truncate = %q", got)
	}
	if got := truncate("", 3); got != "" {
		t.Errorf("empty = %q", got)
	}
}

func TestSegments(t *testing.T) {
	fixedNow(t)
	s := config.New()
	_ = s.Set("cache", "on")
	st := State{Settings: s, Layout: s.Layout(), Home: "/home/me", Git: gitinfo.State{Branch: "main"}}
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"pr changes", `{"pr":{"number":7,"review_state":"changes_requested"}}`, "#7 x"},
		{"mr draft", `{"pr":{"number":7,"kind":"mr","review_state":"draft"}}`, "!7 draft"},
		{"pr pending", `{"pr":{"number":7,"review_state":"pending"}}`, "#7"},
		{"worktree other", `{"workspace":{"git_worktree":"spike"}}`, "wt:spike"},
		{"worktree same", `{"worktree":{"name":"main"}}`, "wt"},
		{"cache cold", `{"prompt_cache":{"warm":false,"caching_observed":true}}`, "cache cold"},
		{"cache off", `{"prompt_cache":{"warm":true,"caching_observed":false}}`, ""},
		{"limit quiet", `{"rate_limits":{"five_hour":{"used_percentage":40,"resets_at":1}}}`, "5h 40%"},
		{"spend", `{"rate_limits":{"spend_limit":{"used_percentage":95,"resets_at":1}}}`, "spend 95% (now)"},
	}
	for _, c := range cases {
		p, err := payload.Parse([]byte(c.doc))
		if err != nil {
			t.Fatal(err)
		}
		r := renderer{p: p, st: st, dropped: map[string]bool{}}
		got := plain(r.line("pr worktree cache limits"))
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRepoSegment(t *testing.T) {
	s := config.New()
	st := State{Settings: s, Layout: s.Layout()}
	p, _ := payload.Parse([]byte(`{"workspace":{"repo":{"owner":"acme","name":"storefront"}}}`))
	if got := (&renderer{p: p, st: st, dropped: map[string]bool{}}).segment("repo"); got != "" {
		t.Errorf("repo is off by default, got %q", got)
	}
	_ = s.Set("repo", "on")
	st.Settings = s
	for doc, want := range map[string]string{
		`{"workspace":{"repo":{"host":"github.com","owner":"acme","name":"storefront"}}}`: "acme/storefront",
		`{"workspace":{"repo":{"name":"storefront"}}}`:                                    "storefront",
		`{"workspace":{"repo":{"owner":"acme"}}}`:                                         "",
		`{"workspace":{"repo":{"owner":"a\u001b[2J","name":"b"}}}`:                        "a?[2J/b",
		`{}`: "",
	} {
		p, err := payload.Parse([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		if got := (&renderer{p: p, st: st, dropped: map[string]bool{}}).segment("repo"); got != want {
			t.Errorf("%s: got %q, want %q", doc, got, want)
		}
	}
}

func TestRepoInLayouts(t *testing.T) {
	base := fixedNow(t)
	s := config.New()
	_ = s.Set("repo", "on")
	_ = s.Set("path", "off")
	want := "ctx 61%  5h 94% (1h20)  7d 72% (2d2h)  agents 2  compact 1 | acme/storefront  feature/billing MERGING  wt  #42 + | Fable 5.1 / high / billing-fix  up 2h15"
	if got := plain(Render(samplePayload(t, base), sampleState(s, base))); got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	_ = s.Set("layout", "full")
	st := sampleState(s, base)
	if got, _, _ := strings.Cut(plain(Render(samplePayload(t, base), st)), "\n"); got != "acme/storefront  feature/billing MERGING  wt  #42 +" {
		t.Errorf("full layout first line = %q", got)
	}
}

func TestCostSegment(t *testing.T) {
	s := config.New()
	_ = s.Set("cost", "on")
	p, _ := payload.Parse([]byte(`{}`))
	cases := []struct {
		name string
		c    cost.Totals
		want string
	}{
		{"none", nil, ""},
		{"priced", cost.Totals{"claude-opus-5-5": {USD: 4.123}}, "cost $4.12"},
		{"two models", cost.Totals{"claude-opus-5-5": {USD: 1}, "claude-haiku-4-5": {USD: 0.5}}, "cost $1.50"},
		{"tiny", cost.Totals{"claude-haiku-4-5": {USD: 0.004}}, "cost <$0.01"},
		{"large", cost.Totals{"claude-fable-5-1": {USD: 1234.4}}, "cost $1234"},
		{"part unpriced", cost.Totals{"claude-opus-5-5": {USD: 2}, "claude-new": {Unpriced: 5000}}, "cost $2.00+"},
		{"all unpriced", cost.Totals{"claude-new": {Unpriced: 1_200_000}}, "tok 1.2m"},
		{"zero tokens", cost.Totals{"claude-opus-5-5": {}}, ""},
	}
	for _, c := range cases {
		st := State{Settings: s, Layout: s.Layout(), Activity: transcript.Activity{Cost: c.c}}
		r := renderer{p: p, st: st, dropped: map[string]bool{}}
		if got := plain(r.line("cost")); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// With cost on, the segment follows the budgets and is dropped right after the cache.
func TestCostInLayouts(t *testing.T) {
	base := fixedNow(t)
	s := config.New()
	_ = s.Set("cost", "on")
	st := sampleState(s, base)
	st.Activity.Cost = cost.Totals{"claude-fable-5-1": {USD: 4.12}}
	p := samplePayload(t, base)
	want := "ctx 61%  5h 94% (1h20)  7d 72% (2d2h)  cost $4.12  agents 2  compact 1 | ~/.../utils/sill  feature/billing MERGING  wt  #42 + | Fable 5.1 / high / billing-fix  up 2h15"
	if got := plain(Render(p, st)); got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	st.Width = 60
	if got := plain(Render(p, st)); got != "ctx 61%  5h 94%  7d 72% | sill | Fable 5.1" {
		t.Errorf("narrow: %q", got)
	}

	_ = s.Set("layout", "full")
	st = sampleState(s, base)
	st.Activity.Cost = cost.Totals{"claude-fable-5-1": {USD: 4.12}}
	if got := strings.Split(plain(Render(p, st)), "\n")[1]; got != "ctx 61% (122k/200k)  5h 94% (1h20)  7d 72% (2d2h)  cost $4.12" {
		t.Errorf("full: %q", got)
	}
}

func TestLineSeparators(t *testing.T) {
	p, _ := payload.Parse([]byte(`{"model":{"display_name":"M"},"session_name":"S","version":"1"}`))
	s := config.New()
	_ = s.Set("version", "on")
	r := renderer{p: p, st: State{Settings: s, Layout: s.Layout()}, pal: colors, dropped: map[string]bool{}}
	cases := map[string]string{
		"model / effort / session version": "M / S  v1",
		"model / effort version":           "M  v1",
		"ctx | model":                      "M",
		"model | ctx | session":            "M | S",
		"model | ctx / session":            "M | S",
		"| model |":                        "M",
		"bogus width model":                "M",
	}
	for spec, want := range cases {
		if got := plain(r.line(spec)); got != want {
			t.Errorf("%q: got %q, want %q", spec, got, want)
		}
	}
	if got := r.line("model | session"); got != "M"+Gray+" | "+Reset+"S" {
		t.Errorf("separator should be dimmed, got %q", got)
	}
}

func TestClean(t *testing.T) {
	cases := map[string]string{
		"billing-fix":                      "billing-fix",
		"a\x1b[2J\x1b]0;pwned\x07b":        "a?[2J?]0;pwned?b",
		"two\nlines\r":                     "two?lines?",
		"c1\u009bcsi":                      "c1?csi",
		"rtl\u202eevil":                    "rtl?evil",
		"sep\u2028line":                    "sep?line",
		"bad\xffutf8":                      "bad?utf8",
		"caf\u00e9 \u4e2d\u6587 ok":        "caf\u00e9 \u4e2d\u6587 ok",
		"tab\there":                        "tab?here",
		"":                                 "",
		"\x00":                             "?",
		"ends with esc\x1b":                "ends with esc?",
		"zero\u200bwidth space stays":      "zero\u200bwidth space stays",
		"emoji \U0001F600 stays":           "emoji \U0001F600 stays",
		"del\x7fchar":                      "del?char",
		"\u2066isolate\u2069":              "?isolate?",
		"plain ascii / slashes | and bars": "plain ascii / slashes | and bars",
	}
	for in, want := range cases {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

// Text from the payload reaches the terminal only through Clean: a hostile session name
// or folder cannot inject escape sequences or break the line.
func TestRenderCleansPayloadText(t *testing.T) {
	p, err := payload.Parse([]byte(`{
		"model": {"display_name": "M\u001b[31m"},
		"workspace": {"current_dir": "/tmp/a\u001b]0;title\u0007b"},
		"session_name": "s\u001b[2J\nsecond line",
		"effort": {"level": "hi\r"},
		"version": "1\u0000",
		"worktree": {"name": "w\u001bx"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	s := config.New()
	_ = s.Set("version", "on")
	st := State{Settings: s, Layout: s.Layout(), Git: gitinfo.State{Branch: "main\x1b[0m"}}
	got := Render(p, st)
	if strings.ContainsAny(got, "\x1b\n\r\x07\x00") {
		t.Errorf("control characters reached the output: %q", got)
	}
	if want := "/tmp/a?]0;title?b  main?[0m  wt:w?x | M?[31m / hi? / s?[2J?second line  v1?"; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

// FuzzRender feeds arbitrary payloads through every layout. Rendering never panics, and
// the only control characters in the output are the SGR color sequences sill writes itself
// and the newlines between layout lines.
func FuzzRender(f *testing.F) {
	f.Add([]byte(`{"model":{"display_name":"Fable 5.1"},"workspace":{"current_dir":"/home/me/x"},"context_window":{"used_percentage":61.2,"total_input_tokens":122400,"context_window_size":200000},"rate_limits":{"five_hour":{"used_percentage":94,"resets_at":1}},"pr":{"number":42,"review_state":"approved"}}`), 80, "full")
	f.Add([]byte(`{"session_name":"a\u001b[2J\nb","cwd":"C:\\Users\\me"}`), 0, "compact")
	f.Add([]byte(`{"context_window":{"used_percentage":-1e308},"rate_limits":{"seven_day":{"used_percentage":1e308,"resets_at":-5}}}`), 3, "compact")
	f.Add([]byte(`{"session_name":"\u4e2d\u6587\u4e2d\u6587\u4e2d\u6587","model":{"id":"\U0001F600"}}`), 10, "full")
	f.Fuzz(func(t *testing.T, doc []byte, width int, layout string) {
		p, err := payload.Parse(doc)
		if err != nil {
			return
		}
		s := config.New()
		if s.Set("layout", layout) != nil {
			return
		}
		for _, k := range []string{"cache", "version", "duration"} {
			_ = s.Set(k, "on")
		}
		st := State{Settings: s, Layout: s.Layout(), Home: "/home/me", Width: width % 400, Color: true}
		out := Render(p, st)
		for line := range strings.SplitSeq(plain(out), "\n") {
			if strings.ContainsFunc(line, unicode.IsControl) {
				t.Fatalf("control character in %q", line)
			}
			if st.Width > 1 && visibleWidth(line) > st.Width-1 {
				t.Fatalf("line %q is %d columns, more than %d", line, visibleWidth(line), st.Width-1)
			}
		}
	})
}

// BenchmarkRender is the formatting cost of one render with every segment on, fitted to a
// terminal narrow enough to run most degrade steps.
func BenchmarkRender(b *testing.B) {
	for _, width := range []int{0, 60} {
		b.Run(fmt.Sprintf("width=%d", width), func(b *testing.B) {
			base := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
			p := samplePayload(b, base)
			s := config.New()
			for _, k := range []string{"cache", "version", "duration"} {
				_ = s.Set(k, "on")
			}
			st := sampleState(s, base)
			st.Width = width
			for b.Loop() {
				Render(p, st)
			}
		})
	}
}

func TestPct(t *testing.T) {
	r := renderer{pal: colors}
	cases := []struct {
		v    float64
		want string
	}{{59.4, "59%"}, {59.5, Yellow + "60%" + Reset}, {80, Red + "80%" + Reset}}
	for _, c := range cases {
		if got := r.pct(c.v, 60, 80); got != c.want {
			t.Errorf("pct(%v) = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestSpanUntilAt(t *testing.T) {
	base := fixedNow(t)
	epoch := func(d time.Duration) float64 { return float64(base.Add(d).Unix()) }
	cases := []struct {
		d     time.Duration
		until string
		at    string
	}{
		{-time.Minute, "now", "11:59"},
		{30 * time.Second, "1m", "12:00"},
		{35 * time.Minute, "35m", "12:35"},
		{65 * time.Minute, "1h05", "13:05"},
		{53 * time.Hour, "2d5h", "Sun 17:00"},
	}
	for _, c := range cases {
		if got := until(epoch(c.d)); got != c.until {
			t.Errorf("until(%v) = %q, want %q", c.d, got, c.until)
		}
		if got := at(epoch(c.d)); got != c.at {
			t.Errorf("at(%v) = %q, want %q", c.d, got, c.at)
		}
	}
	if got := span(0); got != "0m" {
		t.Errorf("span(0) = %q", got)
	}
	if got := span(-5); got != "0m" {
		t.Errorf("span(-5) = %q", got)
	}
	if got := resetLabel(epoch(65*time.Minute), "both"); got != "1h05 @ 13:05" {
		t.Errorf("both = %q", got)
	}
	if got := resetLabel(epoch(65*time.Minute), "absolute"); got != "@ 13:05" {
		t.Errorf("absolute = %q", got)
	}
	if got := resetLabel(epoch(65*time.Minute), "relative"); got != "1h05" {
		t.Errorf("relative = %q", got)
	}
}

func TestTokens(t *testing.T) {
	cases := map[int]string{850: "850", 9500: "9.5k", 122400: "122k", 200000: "200k", 1000000: "1m", 1500000: "1.5m",
		388_200_000: "388m", 2_472_500_000: "2.5b", 3_000_000_000: "3b"}
	for n, want := range cases {
		if got := Tokens(n); got != want {
			t.Errorf("Tokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestDisplayPath(t *testing.T) {
	cases := []struct {
		cwd, home string
		level     int
		want      string
	}{
		{"/home/me/source/repos/utils/sill", "/home/me", 0, "~/source/repos/utils/sill"},
		{"/home/me/source/repos/utils/sill", "/home/me", 1, "~/.../utils/sill"},
		{"/home/me/source/repos/utils/sill", "/home/me", 2, "~/.../sill"},
		{"/home/me/source/repos/utils/sill", "/home/me", 3, "sill"},
		{"/home/me/a/b/c", "/home/me", 1, "~/a/b/c"},
		{"/home/me/a/b/c", "/home/me", 2, "~/.../c"},
		{"/home/me/a/b", "/home/me", 2, "~/a/b"},
		{"/home/me", "/home/me", 1, "~"},
		{"/home/me", "/home/me", 3, "~"},
		{"/home/meow/x", "/home/me", 1, "/home/meow/x"},
		{"/usr/local/share/doc/pkg", "/home/me", 1, "/usr/.../doc/pkg"},
		{`C:\Users\me\source\repos\utils\sill`, `C:\Users\me`, 1, "~/.../utils/sill"},
		{`D:\a\b\c\d\e`, `C:\Users\me`, 1, "D:/.../d/e"},
		{"/tmp/x", "", 1, "/tmp/x"},
		{"/", "", 1, "/"},
	}
	for _, c := range cases {
		if got := DisplayPath(c.cwd, c.home, c.level); got != c.want {
			t.Errorf("DisplayPath(%q, %q, %d) = %q, want %q", c.cwd, c.home, c.level, got, c.want)
		}
	}
}
