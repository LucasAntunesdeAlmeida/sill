// Package config holds sill's options: which segments show, how, and on which lines.
// The settings file only ever contains what the user changed.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/LucasAntunesdeAlmeida/sill/internal/atomicfile"
)

// FileName is the settings file inside the Claude config directory.
const FileName = "sill.json"

// Kind is what values an option takes.
type Kind int

const (
	Bool Kind = iota // on / off
	Enum             // one of Option.Values
	Int              // a non-negative integer
)

// Option describes one setting.
type Option struct {
	Name   string
	Def    string
	Kind   Kind
	Values []string // Enum only
	Desc   string
}

// Options is the settings table, in display order.
var Options = []Option{
	{"layout", "compact", Enum, []string{"compact", "full", "custom"}, "compact is one line, full is three, custom uses \"lines\" from the file"},
	{"reset", "relative", Enum, []string{"relative", "absolute", "both"}, "how a limit's reset time is shown"},
	{"color", "on", Bool, nil, "ANSI colors (NO_COLOR in the environment also turns them off)"},
	{"width", "0", Int, nil, "columns to fit each line into; 0 detects the terminal width"},
	{"ctx", "on", Bool, nil, "context window used"},
	{"limits", "on", Bool, nil, "5h / 7d / spend rate limits"},
	{"cache", "off", Bool, nil, "prompt cache: time until the cached prefix goes cold"},
	{"agents", "on", Bool, nil, "background agents still running (read from the transcript)"},
	{"compactions", "on", Bool, nil, "times the context was compacted (read from the transcript)"},
	{"duration", "off", Bool, nil, "time since the session started (read from the transcript)"},
	{"path", "on", Bool, nil, "working directory"},
	{"git", "on", Bool, nil, "branch and merge/rebase state"},
	{"dirty", "off", Bool, nil, "* after the branch when the tree has uncommitted changes (a second git call)"},
	{"worktree", "on", Bool, nil, "worktree name when inside one"},
	{"pr", "on", Bool, nil, "open PR / MR for the branch"},
	{"model", "on", Bool, nil, "model name"},
	{"effort", "on", Bool, nil, "effort level from /effort"},
	{"session", "on", Bool, nil, "session name from /rename"},
	{"version", "off", Bool, nil, "Claude Code version"},
}

// Find returns the option with that name, or nil.
func Find(name string) *Option {
	for i := range Options {
		if Options[i].Name == name {
			return &Options[i]
		}
	}
	return nil
}

// modifiers are the boolean options that change how the line looks instead of naming a
// segment, so they cannot be placed on a line.
var modifiers = map[string]bool{"color": true, "dirty": true}

// IsSegment reports whether name can be placed on a line.
func IsSegment(name string) bool {
	o := Find(name)
	return o != nil && o.Kind == Bool && !modifiers[name]
}

// Layout says which segments go on which line. A line is segment names in order, with
// "|" for a separator bar and "/" to join neighbours with a slash. Wide layouts have room
// for the full path, token counts and every reset time.
type Layout struct {
	Lines []string
	Wide  bool
}

// Presets are the built-in layouts.
var Presets = map[string]Layout{
	"compact": {Lines: []string{
		"ctx limits cache agents compactions | path git worktree pr | model / effort / session duration version",
	}},
	"full": {Lines: []string{
		"path git worktree pr",
		"ctx limits cache",
		"model / effort / session duration agents compactions version",
	}, Wide: true},
}

// Settings holds the values the user set explicitly. Anything else falls back to the
// option's default.
type Settings struct {
	values map[string]string
	Lines  []string // custom layout, one entry per line
}

// New returns settings with every option at its default.
func New() Settings { return Settings{values: map[string]string{}} }

// Get returns an option's value, or its default.
func (s Settings) Get(name string) string {
	if v, ok := s.values[name]; ok {
		return v
	}
	if o := Find(name); o != nil {
		return o.Def
	}
	return ""
}

// On reports whether a boolean option is on.
func (s Settings) On(name string) bool { return s.Get(name) == "on" }

// Int returns an integer option, or 0 for anything else.
func (s Settings) Int(name string) int {
	n, _ := strconv.Atoi(s.Get(name))
	return n
}

// IsDefault reports whether the option was never set explicitly.
func (s Settings) IsDefault(name string) bool {
	_, explicit := s.values[name]
	return !explicit
}

// Set validates and stores a value. Booleans accept on/off, true/false, yes/no, 1/0.
func (s Settings) Set(name, value string) error {
	o := Find(name)
	if o == nil {
		return fmt.Errorf("unknown option %q (run `sill settings` for the list)", name)
	}
	value = strings.ToLower(strings.TrimSpace(value))
	switch o.Kind {
	case Bool:
		switch value {
		case "on", "true", "yes", "1":
			s.values[name] = "on"
		case "off", "false", "no", "0":
			s.values[name] = "off"
		default:
			return fmt.Errorf("%s takes on or off, not %q", name, value)
		}
	case Enum:
		for _, v := range o.Values {
			if v == value {
				s.values[name] = v
				return nil
			}
		}
		return fmt.Errorf("%s takes %s, not %q", name, strings.Join(o.Values, ", "), value)
	case Int:
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("%s takes a whole number, not %q", name, value)
		}
		s.values[name] = strconv.Itoa(n)
	}
	return nil
}

// Unset returns an option to its default, or with "lines" drops the custom lines.
func (s *Settings) Unset(name string) error {
	if name == "lines" {
		s.Lines = nil
		return nil
	}
	if Find(name) == nil {
		return fmt.Errorf("unknown option %q (run `sill settings` for the list)", name)
	}
	delete(s.values, name)
	return nil
}

// Layout resolves the active layout. A custom layout without lines falls back to compact.
func (s Settings) Layout() Layout {
	name := s.Get("layout")
	if name == "custom" {
		if len(s.Lines) > 0 {
			return Layout{Lines: s.Lines, Wide: len(s.Lines) > 1}
		}
		name = "compact"
	}
	return Presets[name]
}

// Dir is the Claude config directory: $CLAUDE_CONFIG_DIR, or ~/.claude.
func Dir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

// Path is the settings file location.
func Path() string { return filepath.Join(Dir(), FileName) }

// Load reads the settings file. A missing file means defaults; a broken one is reported
// so a typo does not silently reset everything.
func Load() (Settings, error) {
	s := New()
	data, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := s.Parse(data); err != nil {
		return s, fmt.Errorf("%s: %w", Path(), err)
	}
	return s, nil
}

// Parse applies a settings document. Unknown keys are ignored and invalid values keep
// the default, so an old file never breaks a newer sill.
func (s *Settings) Parse(data []byte) error {
	_, err := s.parse(data)
	return err
}

// Check reports what Parse would silently ignore in a settings document: unknown keys,
// invalid values and names in "lines" that are not segments.
func Check(data []byte) ([]string, error) {
	s := New()
	return s.parse(data)
}

func (s *Settings) parse(data []byte) ([]string, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	var problems []string
	for _, k := range sortedKeys(raw) {
		v := raw[k]
		if k == "lines" {
			problems = append(problems, s.parseLines(v)...)
			continue
		}
		if Find(k) == nil {
			problems = append(problems, fmt.Sprintf("unknown option %q, ignored", k))
			continue
		}
		var text string
		switch t := v.(type) {
		case bool:
			text = "off"
			if t {
				text = "on"
			}
		case string:
			text = t
		case float64:
			text = strconv.Itoa(int(t))
		default:
			problems = append(problems, fmt.Sprintf("%s has a value of the wrong type, using the default", k))
			continue
		}
		if err := s.Set(k, text); err != nil {
			problems = append(problems, err.Error()+", using the default")
		}
	}
	if s.Get("layout") == "custom" && len(s.Lines) == 0 {
		problems = append(problems, `layout is custom but there are no "lines", showing compact`)
	}
	return problems, nil
}

func (s *Settings) parseLines(v any) []string {
	s.Lines = nil
	items, ok := v.([]any)
	if !ok {
		return []string{`"lines" should be a list of strings`}
	}
	var problems []string
	for i, it := range items {
		line, ok := it.(string)
		if !ok {
			problems = append(problems, fmt.Sprintf("line %d is not a string, ignored", i+1))
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		for tok := range strings.FieldsSeq(line) {
			if tok != "|" && tok != "/" && !IsSegment(tok) {
				problems = append(problems, fmt.Sprintf("line %d names %q, which is not a segment", i+1, tok))
			}
		}
		s.Lines = append(s.Lines, line)
	}
	return problems
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Save writes the explicitly set options in table order. The file is replaced in one step
// so a render running at the same moment never reads half of it.
func (s Settings) Save() error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	return atomicfile.Write(Path(), s.Marshal(), 0o644)
}

// Marshal renders the settings document: booleans as JSON booleans, integers as numbers.
func (s Settings) Marshal() []byte {
	var entries []string
	for _, o := range Options {
		v, ok := s.values[o.Name]
		if !ok {
			continue
		}
		switch o.Kind {
		case Bool:
			entries = append(entries, fmt.Sprintf("  %q: %t", o.Name, v == "on"))
		case Int:
			entries = append(entries, fmt.Sprintf("  %q: %s", o.Name, v))
		default:
			entries = append(entries, fmt.Sprintf("  %q: %s", o.Name, jsonString(v)))
		}
	}
	if len(s.Lines) > 0 {
		quoted := make([]string, len(s.Lines))
		for i, l := range s.Lines {
			quoted[i] = "    " + jsonString(l)
		}
		entries = append(entries, "  \"lines\": [\n"+strings.Join(quoted, ",\n")+"\n  ]")
	}
	var b strings.Builder
	b.WriteString("{\n")
	b.WriteString(strings.Join(entries, ",\n"))
	if len(entries) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return []byte(b.String())
}

// jsonString quotes s as a JSON string. Go's %q is not JSON: it writes \x01 and \U escapes.
func jsonString(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// Describe renders the options table with current values.
func (s Settings) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-12s %-10s %s\n", "option", "value", "shows")
	for _, o := range Options {
		v := s.Get(o.Name)
		if !s.IsDefault(o.Name) {
			v += "*"
		}
		fmt.Fprintf(&b, "%-12s %-10s %s\n", o.Name, v, o.Desc)
	}
	if len(s.Lines) > 0 {
		b.WriteString("\ncustom lines:\n")
		for _, l := range s.Lines {
			fmt.Fprintf(&b, "  %s\n", l)
		}
	}
	b.WriteString("\n* changed from the default. Booleans take on or off.\n")
	fmt.Fprintf(&b, "File: %s\n", Path())
	return b.String()
}
