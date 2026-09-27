package install

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	for _, cmd := range []string{`"/usr/local/bin/sill"`, `sill`, `"C:/x/sill.exe"`} {
		doc := `{"statusLine": {"type": "command", "command": ` + mustJSON(cmd) + `}}`
		if _, changed, _ := RemoveStatusLine([]byte(doc)); !changed {
			t.Errorf("%s not recognised as sill", cmd)
		}
	}
	if _, changed, _ := RemoveStatusLine([]byte(`{"statusLine": {"type": "command", "command": "sillier"}}`)); changed {
		t.Error("a command merely containing sill was removed")
	}
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
	for _, want := range []string{"Updated statusLine", "statusline-command.sh is no longer used", "Tip: add", "Restart Claude Code"} {
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
	if !strings.Contains(out.String(), "Removed statusLine") || !strings.Contains(out.String(), "sill.json") {
		t.Errorf("uninstall:\n%s", out.String())
	}
	data, _ = os.ReadFile(filepath.Join(dir, "settings.json"))
	if strings.Contains(string(data), "statusLine") || !strings.Contains(string(data), `"model"`) {
		t.Errorf("after uninstall:\n%s", data)
	}
	out.Reset()
	if err := Uninstall(dir, &out); err != nil || !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("second uninstall: err=%v out=%q", err, out.String())
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
