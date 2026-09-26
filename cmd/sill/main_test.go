package main

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestRenderFromStdin(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NO_COLOR", "")
	in := strings.NewReader(`{"model":{"display_name":"Fable 5.1"},"workspace":{"current_dir":"/tmp/nowhere"},"context_window":{"used_percentage":12}}`)
	var out bytes.Buffer
	if err := run(nil, in, &out); err != nil {
		t.Fatal(err)
	}
	if got := ansi.ReplaceAllString(out.String(), ""); got != "ctx 12% | /tmp/nowhere | Fable 5.1" {
		t.Errorf("got %q", got)
	}
	if !strings.Contains(out.String(), "\x1b[") {
		t.Error("colors should be on by default")
	}
	if err := run(nil, strings.NewReader("{"), &out); err == nil {
		t.Error("broken payload should be reported")
	}
}

func TestNoColor(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NO_COLOR", "1")
	var out bytes.Buffer
	if err := run(nil, strings.NewReader(`{"model":{"id":"m"},"cwd":"/x"}`), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Errorf("NO_COLOR ignored: %q", out.String())
	}
	t.Setenv("NO_COLOR", "")
	if err := run([]string{"set", "color", "off"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"demo"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Errorf("color off ignored: %q", out.String())
	}
}

func TestWidthSetting(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var out bytes.Buffer
	if err := run([]string{"set", "width", "60"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"demo"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimRight(ansi.ReplaceAllString(out.String(), ""), "\n")
	if len(line) > 59 {
		t.Errorf("demo did not fit into 60 columns (%d): %q", len(line), line)
	}
	if !strings.HasPrefix(line, "ctx 61%") || !strings.Contains(line, "Fable 5.1") {
		t.Errorf("fitting should keep budgets and the model: %q", line)
	}
}

func TestSetAndSettings(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var out bytes.Buffer
	if err := run([]string{"set", "layout", "full", "cache", "on"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "layout = full\ncache = on\n" {
		t.Errorf("set output %q", out.String())
	}
	out.Reset()
	if err := run([]string{"settings"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"layout       full*", "cache        on*", "Detected terminal width:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("settings output missing %q:\n%s", want, out.String())
		}
	}
	for _, bad := range [][]string{{"set"}, {"set", "layout"}, {"set", "bogus", "on"}, {"set", "layout", "wide"}, {"set", "width", "-1"}} {
		if err := run(bad, nil, &out); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	out.Reset()
	if err := run([]string{"set", "layout", "custom"}, nil, &out); err != nil || !strings.Contains(out.String(), "has no \"lines\"") {
		t.Errorf("custom without lines: err=%v out=%q", err, out.String())
	}
}

func TestDemo(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NO_COLOR", "")
	var out bytes.Buffer
	if err := run([]string{"set", "width", "300", "dirty", "on", "duration", "on"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"demo"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	got := ansi.ReplaceAllString(out.String(), "")
	for _, want := range []string{"ctx 61%", "5h 94% (1h20)", "agents 2", "compact 1", "up 2h15", "feature/billing* MERGING", "wt", "#42 +", "Fable 5.1 / high / billing-fix"} {
		if !strings.Contains(got, want) {
			t.Errorf("demo missing %q:\n%s", want, got)
		}
	}
	for _, r := range got {
		if r > 127 {
			t.Fatalf("non-ASCII output %q", r)
		}
	}
}

func TestUninstallWithoutInstall(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var out bytes.Buffer
	if err := run([]string{"uninstall"}, nil, &out); err != nil || !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("uninstall: err=%v out=%q", err, out.String())
	}
}

func TestHelpAndUnknown(t *testing.T) {
	var out bytes.Buffer
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		out.Reset()
		if err := run(args, nil, &out); err != nil || !strings.Contains(out.String(), "Usage:") {
			t.Errorf("%v: err=%v out=%q", args, err, out.String())
		}
	}
	out.Reset()
	if err := run([]string{"version"}, nil, &out); err != nil || !strings.HasPrefix(out.String(), "sill ") {
		t.Errorf("version: err=%v out=%q", err, out.String())
	}
	if err := run([]string{"frobnicate"}, nil, &out); err == nil || !strings.Contains(err.Error(), "Usage:") {
		t.Errorf("unknown command: %v", err)
	}
}
