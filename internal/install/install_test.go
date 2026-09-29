package install

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

const existing = `{
  "model": "claude-fable-5-1[1m]",
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{"type": "command", "command": "rtk hook claude"}]
      }
    ]
  },
  "statusLine": {
    "type": "command",
    "command": "powershell -File old.ps1",
    "padding": 0
  },
  "tui": "fullscreen"
}`

func TestSetStatusLine(t *testing.T) {
	out, changed, err := SetStatusLine([]byte(existing), `"C:/Users/me/go/bin/sill.exe"`)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected a change")
	}
	text := string(out)
	for _, key := range []string{`"model"`, `"hooks"`, `"statusLine"`, `"tui"`} {
		if !strings.Contains(text, key) {
			t.Errorf("missing %s", key)
		}
	}
	if strings.Index(text, `"hooks"`) > strings.Index(text, `"statusLine"`) || strings.Index(text, `"statusLine"`) > strings.Index(text, `"tui"`) {
		t.Errorf("key order changed:\n%s", text)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, text)
	}
	sl := doc["statusLine"].(map[string]any)
	if sl["command"] != `"C:/Users/me/go/bin/sill.exe"` || sl["type"] != "command" || sl["padding"] != float64(0) {
		t.Errorf("statusLine = %v", sl)
	}
	if doc["hooks"].(map[string]any)["PreToolUse"] == nil {
		t.Error("hooks lost")
	}

	again, changed, err := SetStatusLine(out, `"C:/Users/me/go/bin/sill.exe"`)
	if err != nil || changed || string(again) != text {
		t.Errorf("second run changed=%v err=%v", changed, err)
	}
}

func TestSetStatusLineFresh(t *testing.T) {
	out, changed, err := SetStatusLine(nil, `"/usr/local/bin/sill"`)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	want := "{\n  \"statusLine\": {\n    \"command\": \"\\\"/usr/local/bin/sill\\\"\",\n    \"type\": \"command\"\n  }\n}\n"
	if string(out) != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
	if _, _, err := SetStatusLine([]byte(`[1]`), "x"); err == nil {
		t.Error("array accepted as settings")
	}
	if _, _, err := SetStatusLine([]byte(`{"a":`), "x"); err == nil {
		t.Error("truncated document accepted")
	}
}

func TestRemoveStatusLine(t *testing.T) {
	// Someone else's status line stays.
	out, changed, err := RemoveStatusLine([]byte(existing))
	if err != nil || changed || string(out) != existing {
		t.Errorf("foreign statusLine: changed=%v err=%v", changed, err)
	}
	// No entry at all.
	out, changed, err = RemoveStatusLine([]byte(`{"model": "x"}`))
	if err != nil || changed || string(out) != `{"model": "x"}` {
		t.Errorf("no statusLine: changed=%v err=%v", changed, err)
	}
	// Ours goes, everything else stays in order.
	withSill, _, _ := SetStatusLine([]byte(existing), `"C:/Users/me/go/bin/sill.exe"`)
	out, changed, err = RemoveStatusLine(withSill)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["statusLine"]; ok {
		t.Error("statusLine still present")
	}
	if doc["model"] != "claude-fable-5-1[1m]" || doc["tui"] != "fullscreen" || doc["hooks"] == nil {
		t.Errorf("other settings damaged: %v", doc)
	}
	if strings.Index(string(out), `"model"`) > strings.Index(string(out), `"hooks"`) {
		t.Error("key order changed")
	}
	for _, cmd := range []string{`"/usr/local/bin/sill"`, `sill`, `"C:/x/sill.exe"`, `"C:/x/sill-windows-amd64.exe"`, `"/opt/sill-linux-arm64"`, `C:\x\SILL.EXE`} {
		doc := `{"statusLine": {"type": "command", "command": ` + mustJSON(cmd) + `}}`
		if _, changed, _ := RemoveStatusLine([]byte(doc)); !changed {
			t.Errorf("%s not recognised as sill", cmd)
		}
	}
	if _, changed, _ := RemoveStatusLine([]byte(`{"statusLine": {"type": "command", "command": "sillier"}}`)); changed {
		t.Error("a command merely containing sill was removed")
	}
}

func TestIsSill(t *testing.T) {
	cases := map[string]bool{
		`"C:/Users/me/go/bin/sill.exe"`:       true,
		`"C:\Users\me\go\bin\SILL.EXE"`:       true,
		`"/opt/my tools/sill" --debug`:        true,
		`sill`:                                true,
		`sill-darwin-arm64`:                   true,
		`"/usr/local/bin/sill_v1"`:            true,
		`sillier`:                             false,
		`"/usr/local/bin/not-sill"`:           false,
		`"/usr/local/bin/sill.exe.bak"`:       false,
		`powershell -File C:/x/sill.ps1`:      false,
		`bash -c "sill"`:                      false,
		`"C:/Program Files/sill/other.exe"`:   false,
		`/home/me/.claude/statusline-sill.sh`: false,
		``:                                    false,
	}
	for cmd, want := range cases {
		if got := IsSill(cmd); got != want {
			t.Errorf("IsSill(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestIsTempBuild(t *testing.T) {
	cases := map[string]bool{
		`C:\Users\me\AppData\Local\Temp\go-build484002928\b001\exe\sill.exe`: true,
		"/tmp/go-build123/b001/exe/sill":                                     true,
		"/home/me/go/bin/sill":                                               false,
		`C:\Users\me\go\bin\sill.exe`:                                        false,
		"/home/me/src/go-builder/sill":                                       false,
		"/home/me/go-build/sill":                                             false,
	}
	for exe, want := range cases {
		if got := IsTempBuild(exe); got != want {
			t.Errorf("IsTempBuild(%q) = %v, want %v", exe, got, want)
		}
	}
	if err := Run(t.TempDir(), "/tmp/go-build1/b001/exe/sill", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "go install") {
		t.Errorf("a go run build should be refused, got %v", err)
	}
}

func TestCurrent(t *testing.T) {
	dir := t.TempDir()
	if cmd, err := Current(dir); cmd != "" || err != nil {
		t.Errorf("no settings.json: %q, %v", cmd, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if cmd, err := Current(dir); cmd != "powershell -File old.ps1" || err != nil {
		t.Errorf("existing: %q, %v", cmd, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"a":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Current(dir); err == nil {
		t.Error("broken settings.json should be reported")
	}
}

func TestParseEntriesIsStrict(t *testing.T) {
	for _, doc := range []string{`{"a": 1`, `{"a": 1}{"b": 2}`, `{"a": 1} x`} {
		if _, err := parseEntries([]byte(doc)); err == nil {
			t.Errorf("%q accepted; rewriting it would drop or invent content", doc)
		}
	}
	if entries, err := parseEntries([]byte("{\"a\": 1}\n\n")); err != nil || len(entries) != 1 {
		t.Errorf("trailing whitespace: %v, %v", entries, err)
	}
}

// FuzzSetStatusLine checks the promise install makes about the user's settings.json: every
// other key survives with the same value and in the same order, the entry is idempotent,
// and uninstall takes out exactly what install put in.
func FuzzSetStatusLine(f *testing.F) {
	f.Add([]byte(existing), `"C:/Users/me/go/bin/sill.exe"`)
	f.Add([]byte(`{}`), `"/usr/local/bin/sill"`)
	f.Add([]byte(`{"a": [1, {"b": null}], "statusLine": 3, "z": "\u2028<&>"}`), `sill`)
	f.Add([]byte(``), `"/opt/sill-linux-amd64"`)
	f.Fuzz(func(t *testing.T, raw []byte, command string) {
		if !utf8.ValidString(command) {
			return // JSON cannot carry it; sill only ever passes a path
		}
		out, _, err := SetStatusLine(raw, command)
		if err != nil {
			return
		}
		before, err := parseEntries(raw)
		if err != nil {
			t.Fatalf("SetStatusLine accepted a document parseEntries rejects: %v", err)
		}
		after, err := parseEntries(out)
		if err != nil {
			t.Fatalf("output does not parse: %v\n%s", err, out)
		}
		others := func(entries []entry) []entry {
			var kept []entry
			for _, e := range entries {
				if e.key != "statusLine" {
					kept = append(kept, e)
				}
			}
			return kept
		}
		b, a := others(before), others(after)
		if len(a) != len(b) {
			t.Fatalf("other keys: %d before, %d after", len(b), len(a))
		}
		for i := range b {
			if a[i].key != b[i].key || !sameJSON(t, a[i].val, b[i].val) {
				t.Fatalf("key %q changed: %s -> %s", b[i].key, b[i].val, a[i].val)
			}
		}
		if got, err := statusCommand(out); err != nil || got != command {
			t.Fatalf("command = %q, want %q (%v)", got, command, err)
		}
		if again, changed, err := SetStatusLine(out, command); err != nil || changed || !bytes.Equal(again, out) {
			t.Fatalf("second install changed=%v err=%v", changed, err)
		}
		if IsSill(command) {
			removed, changed, err := RemoveStatusLine(out)
			if err != nil || !changed {
				t.Fatalf("uninstall changed=%v err=%v", changed, err)
			}
			left, _ := parseEntries(removed)
			if len(others(left)) != len(b) {
				t.Fatalf("uninstall left %d other keys, want %d", len(others(left)), len(b))
			}
		}
	})
}

// statusCommand reads the statusLine command out of a document in memory.
func statusCommand(raw []byte) (string, error) {
	entries, err := parseEntries(raw)
	if err != nil {
		return "", err
	}
	_, current := findStatusLine(entries)
	var cmd string
	err = json.Unmarshal(current["command"], &cmd)
	return cmd, err
}

func sameJSON(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xs, _ := json.Marshal(x)
	ys, _ := json.Marshal(y)
	return bytes.Equal(xs, ys)
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestRunAndUninstall(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "bin", "sill")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	const original = `{"model": "x"}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "statusline-command.sh"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sill.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")

	var out bytes.Buffer
	if err := Run(dir, exe, &out); err != nil {
		t.Fatal(err)
	}
	log := out.String()
	for _, want := range []string{"Updated statusLine", "Added a SessionEnd hook", "statusline-command.sh is no longer used", "Tip: add", "Restart Claude Code"} {
		if !strings.Contains(log, want) {
			t.Errorf("missing %q in:\n%s", want, log)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["model"] != "x" {
		t.Error("other settings lost")
	}
	cmd := doc["statusLine"].(map[string]any)["command"].(string)
	if !strings.HasPrefix(cmd, `"`) || strings.Contains(cmd, `\`) {
		t.Errorf("command should be quoted with forward slashes: %q", cmd)
	}
	if hooks, err := HookCommands(dir); err != nil || len(hooks) != 1 || hooks[0] != cmd+" hook" {
		t.Errorf("hooks = %q, %v; want the same binary with hook", hooks, err)
	}
	backup, err := os.ReadFile(filepath.Join(dir, BackupName))
	if err != nil || string(backup) != original {
		t.Errorf("backup = %q, %v; want the original file", backup, err)
	}
	if stray, _ := filepath.Glob(filepath.Join(dir, "settings.json.*.bak")); len(stray) != 1 {
		t.Errorf("expected exactly one backup file, got %v", stray)
	}

	out.Reset()
	if err := Run(dir, exe, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already points") {
		t.Errorf("second run:\n%s", out.String())
	}
	if again, _ := os.ReadFile(filepath.Join(dir, BackupName)); string(again) != original {
		t.Errorf("no-op run must not touch the backup, got %q", again)
	}

	out.Reset()
	if err := Uninstall(dir, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Removed statusLine", "Removed the SessionEnd hook", "sill.json"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("uninstall lacks %q:\n%s", want, out.String())
		}
	}
	data, _ = os.ReadFile(filepath.Join(dir, "settings.json"))
	if strings.Contains(string(data), "statusLine") || strings.Contains(string(data), "hooks") || !strings.Contains(string(data), `"model"`) {
		t.Errorf("after uninstall:\n%s", data)
	}
	out.Reset()
	if err := Uninstall(dir, &out); err != nil || !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("second uninstall: err=%v out=%q", err, out.String())
	}
}

func TestIsSillHook(t *testing.T) {
	for cmd, want := range map[string]bool{
		`"C:/Users/me/go/bin/sill.exe" hook`: true,
		`/usr/local/bin/sill hook`:           true,
		`sill hook `:                         true,
		`"/opt/sill-linux-amd64" hook`:       true,
		`"C:/Users/me/go/bin/sill.exe"`:      false,
		`sill hooks`:                         false,
		`other hook`:                         false,
		`rtk hook claude`:                    false,
	} {
		if got := IsSillHook(cmd); got != want {
			t.Errorf("IsSillHook(%q) = %v, want %v", cmd, got, want)
		}
	}
}

const withHooks = `{
  "model": "x",
  "hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "rtk hook claude"}]}],
    "SessionEnd": [
      {"hooks": [{"type": "command", "command": "notify-send bye"}, {"type": "command", "command": "\"/old/sill\" hook"}]}
    ],
    "Stop": [{"hooks": [{"type": "command", "command": "say done"}]}]
  },
  "tui": "fullscreen"
}`

func TestSetHook(t *testing.T) {
	const cmd = `"/new/sill" hook`
	out, changed, err := SetHook([]byte(withHooks), cmd)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	text := string(out)
	// Other hooks and the order of keys and events stay; the old sill hook is replaced.
	for _, want := range []string{"rtk hook claude", "notify-send bye", "say done", "/new/sill"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "/old/sill") {
		t.Errorf("old sill hook kept:\n%s", text)
	}
	order := []string{`"model"`, `"hooks"`, `"PreToolUse"`, `"SessionEnd"`, `"Stop"`, `"tui"`}
	for i := 1; i < len(order); i++ {
		if strings.Index(text, order[i-1]) > strings.Index(text, order[i]) {
			t.Errorf("%s moved after %s:\n%s", order[i-1], order[i], text)
		}
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct{ Type, Command string }
		}
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	groups := doc.Hooks[HookEvent]
	if len(groups) != 2 || len(groups[0].Hooks) != 1 || groups[1].Hooks[0].Command != cmd || groups[1].Hooks[0].Type != "command" {
		t.Errorf("SessionEnd = %+v", groups)
	}

	again, changed, err := SetHook(out, cmd)
	if err != nil || changed || string(again) != text {
		t.Errorf("second run changed=%v err=%v", changed, err)
	}

	fresh, changed, err := SetHook([]byte(`{"model":"x"}`), cmd)
	want := "{\n  \"model\": \"x\",\n  \"hooks\": {\n    \"SessionEnd\": [\n      {\n        \"hooks\": [\n          {\n            \"command\": \"\\\"/new/sill\\\" hook\",\n            \"type\": \"command\"\n          }\n        ]\n      }\n    ]\n  }\n}\n"
	if err != nil || !changed || string(fresh) != want {
		t.Errorf("fresh:\n%s\nwant:\n%s", fresh, want)
	}
	if _, _, err := SetHook([]byte(`{"hooks": []}`), cmd); err == nil {
		t.Error("hooks that is not an object accepted")
	}
	if _, _, err := SetHook([]byte(`{"hooks": {"SessionEnd": {}}}`), cmd); err == nil {
		t.Error("SessionEnd that is not a list accepted")
	}
}

func TestRemoveHook(t *testing.T) {
	out, changed, err := RemoveHook([]byte(withHooks))
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if strings.Contains(string(out), "sill") || !strings.Contains(string(out), "notify-send bye") {
		t.Errorf("after remove:\n%s", out)
	}
	// Removing the only hook drops the event and then the empty hooks setting.
	only, _, _ := SetHook([]byte(`{"model":"x"}`), `"/new/sill" hook`)
	out, changed, err = RemoveHook(only)
	if err != nil || !changed || string(out) != "{\n  \"model\": \"x\"\n}\n" {
		t.Errorf("remove the only hook: %v %v\n%s", changed, err, out)
	}
	same, changed, err := RemoveHook(out)
	if err != nil || changed || string(same) != string(out) {
		t.Errorf("nothing to remove: changed=%v err=%v", changed, err)
	}
}

func TestPathHint(t *testing.T) {
	var out bytes.Buffer
	pathHint("/opt/sill", strings.Join([]string{"/usr/bin", "/opt/sill/"}, string(os.PathListSeparator)), &out)
	if out.Len() != 0 {
		t.Errorf("dir on PATH should print nothing, got %q", out.String())
	}
	pathHint("/opt/sill", "/usr/bin", &out)
	if !strings.Contains(out.String(), filepath.Clean("/opt/sill")) {
		t.Errorf("hint missing: %q", out.String())
	}
}
