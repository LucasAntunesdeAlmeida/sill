package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSet(t *testing.T) {
	s := New()
	if !s.On("ctx") || s.On("cache") || s.Get("layout") != "compact" || s.Int("width") != 0 {
		t.Fatal("defaults are wrong")
	}
	for _, v := range []string{"off", "false", "no", "0", "OFF"} {
		if err := s.Set("ctx", v); err != nil || s.On("ctx") {
			t.Errorf("set ctx %q: err=%v on=%v", v, err, s.On("ctx"))
		}
	}
	if err := s.Set("ctx", "maybe"); err == nil {
		t.Error("bad boolean accepted")
	}
	if err := s.Set("reset", "Absolute"); err != nil || s.Get("reset") != "absolute" {
		t.Errorf("enum set failed: %v", err)
	}
	if err := s.Set("reset", "never"); err == nil {
		t.Error("bad enum accepted")
	}
	if err := s.Set("width", "120"); err != nil || s.Int("width") != 120 {
		t.Errorf("int set failed: %v", err)
	}
	for _, bad := range []string{"-1", "wide", "1.5"} {
		if err := s.Set("width", bad); err == nil {
			t.Errorf("width %q accepted", bad)
		}
	}
	if err := s.Set("bogus", "on"); err == nil {
		t.Error("unknown option accepted")
	}
	if s.IsDefault("ctx") || !s.IsDefault("git") {
		t.Error("IsDefault is wrong")
	}
	if s.Get("bogus") != "" || s.Int("layout") != 0 {
		t.Error("unknown or non-integer options should read as empty / 0")
	}
}

func TestLayout(t *testing.T) {
	s := New()
	if l := s.Layout(); l.Wide || len(l.Lines) != 1 {
		t.Errorf("compact = %+v", l)
	}
	_ = s.Set("layout", "full")
	if l := s.Layout(); !l.Wide || len(l.Lines) != 3 {
		t.Errorf("full = %+v", l)
	}
	_ = s.Set("layout", "custom")
	if l := s.Layout(); l.Wide || len(l.Lines) != 1 {
		t.Errorf("custom without lines should be compact, got %+v", l)
	}
	s.Lines = []string{"path", "ctx"}
	if l := s.Layout(); !l.Wide || len(l.Lines) != 2 {
		t.Errorf("custom = %+v", l)
	}
	s.Lines = []string{"path ctx"}
	if l := s.Layout(); l.Wide {
		t.Error("one custom line should not be wide")
	}
}

func TestPresetsNameRealSegments(t *testing.T) {
	for name, l := range Presets {
		for _, line := range l.Lines {
			for tok := range strings.FieldsSeq(line) {
				if tok == "|" || tok == "/" {
					continue
				}
				if !IsSegment(tok) {
					t.Errorf("preset %s names %q, which is not a segment", name, tok)
				}
			}
		}
	}
}

func TestIsSegment(t *testing.T) {
	for name, want := range map[string]bool{"ctx": true, "path": true, "color": false, "dirty": false, "layout": false, "width": false, "bogus": false} {
		if got := IsSegment(name); got != want {
			t.Errorf("IsSegment(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestCheck(t *testing.T) {
	problems, err := Check([]byte(`{"colour": false, "git": "sideways", "pr": [1], "layout": "custom", "lines": ["path bogus | model", 3, "dirty"]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{ // in key order
		`unknown option "colour", ignored`,
		`git takes on or off, not "sideways", using the default`,
		`line 1 names "bogus", which is not a segment`,
		`line 2 is not a string, ignored`,
		`line 3 names "dirty", which is not a segment`,
		`pr has a value of the wrong type, using the default`,
	}
	if strings.Join(problems, "\n") != strings.Join(want, "\n") {
		t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(problems, "\n"), strings.Join(want, "\n"))
	}
	if problems, _ := Check([]byte(`{"layout": "custom"}`)); len(problems) != 1 || !strings.Contains(problems[0], "no \"lines\"") {
		t.Errorf("custom without lines: %q", problems)
	}
	if problems, _ := Check([]byte(`{"lines": "path"}`)); len(problems) != 1 {
		t.Errorf("lines as a string: %q", problems)
	}
	if problems, err := Check([]byte(`{"ctx": false, "width": 90}`)); err != nil || len(problems) != 0 {
		t.Errorf("clean file: %q, %v", problems, err)
	}
	if _, err := Check([]byte(`{bad`)); err == nil {
		t.Error("broken JSON should be an error")
	}
}

func TestUnset(t *testing.T) {
	s := New()
	_ = s.Set("cache", "on")
	s.Lines = []string{"path"}
	if err := s.Unset("cache"); err != nil || s.On("cache") || !s.IsDefault("cache") {
		t.Errorf("unset cache: err=%v on=%v", err, s.On("cache"))
	}
	if err := s.Unset("lines"); err != nil || s.Lines != nil {
		t.Errorf("unset lines: err=%v lines=%v", err, s.Lines)
	}
	if err := s.Unset("bogus"); err == nil {
		t.Error("unknown option accepted")
	}
}

func TestParseSaveLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	s := New()
	err := s.Parse([]byte(`{"ctx": false, "reset": "both", "width": 100, "layout": "custom", "lines": ["path git", "ctx", ""], "unknown": 1, "git": "sideways", "pr": 3}`))
	if err != nil {
		t.Fatal(err)
	}
	if s.On("ctx") || s.Get("reset") != "both" || s.Int("width") != 100 || len(s.Lines) != 2 || !s.On("git") || !s.On("pr") {
		t.Errorf("parsed = %+v", s)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"layout\": \"custom\",\n  \"reset\": \"both\",\n  \"width\": 100,\n  \"ctx\": false,\n  \"lines\": [\n    \"path git\",\n    \"ctx\"\n  ]\n}\n"
	if string(data) != want {
		t.Errorf("saved:\n%s\nwant:\n%s", data, want)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.On("ctx") || loaded.Get("reset") != "both" || loaded.Int("width") != 100 || len(loaded.Lines) != 2 {
		t.Errorf("reloaded = %+v", loaded)
	}
	if !loaded.IsDefault("git") {
		t.Error("git should still be a default after a round trip")
	}

	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Error("broken file should be reported")
	}
	if string(New().Marshal()) != "{\n}\n" {
		t.Errorf("empty document = %q", New().Marshal())
	}
}

func TestLoadMissing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	s, err := Load()
	if err != nil || !s.On("ctx") {
		t.Errorf("missing file: err=%v settings=%+v", err, s)
	}
}

func TestDescribe(t *testing.T) {
	s := New()
	_ = s.Set("cache", "on")
	s.Lines = []string{"path"}
	out := s.Describe()
	for _, want := range []string{"cache        on*", "layout       compact ", "width        0 ", "custom lines:\n  path\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(`{"ctx": false, "reset": "both", "width": 100, "layout": "custom", "lines": ["path git", "ctx"]}`))
	f.Add([]byte(`{"lines": [1, null, "x"], "width": -3, "color": "maybe"}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"lines": ["path\u0001 <git> & \"model\""]}`)) // %q once wrote this as invalid JSON
	f.Fuzz(func(t *testing.T, data []byte) {
		s := New()
		if s.Parse(data) != nil {
			return
		}
		// Whatever was accepted survives a save and a reload unchanged.
		again := New()
		if err := again.Parse(s.Marshal()); err != nil {
			t.Fatalf("Marshal produced unreadable JSON: %v\n%s", err, s.Marshal())
		}
		if string(again.Marshal()) != string(s.Marshal()) {
			t.Fatalf("round trip changed the settings:\n%s\n%s", s.Marshal(), again.Marshal())
		}
	})
}
