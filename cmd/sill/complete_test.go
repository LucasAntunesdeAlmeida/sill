package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/LucasAntunesdeAlmeida/sill/internal/config"
)

func completeWords(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := run(append([]string{"__complete"}, args...), nil, &out); err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

func TestComplete(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"0"}, strings.Join(commands, " ")},
		{[]string{"0", ""}, strings.Join(commands, " ")},
		{[]string{"0", "se"}, "set settings"},
		{[]string{"0", "hook"}, ""},
		{[]string{"1", "set", "la"}, "layout"},
		{[]string{"1", "set", "LA"}, "layout"},
		{[]string{"2", "set", "layout"}, "compact custom full"},
		{[]string{"2", "set", "layout", "f"}, "full"},
		{[]string{"2", "set", "cache"}, "off on"},
		{[]string{"2", "set", "width"}, ""},
		{[]string{"2", "set", "bogus"}, ""},
		{[]string{"3", "set", "layout", "full", "ca"}, "cache"},
		{[]string{"3", "set", "layout", "full", "l"}, "limits"},
		{[]string{"4", "set", "layout", "full", "cache"}, "off on"},
		{[]string{"1", "unset", "li"}, "limits lines"},
		{[]string{"2", "unset", "lines", "li"}, "limits"},
		{[]string{"1", "cost"}, dirsDirective},
		{[]string{"1", "cost", "sr"}, dirsDirective},
		{[]string{"2", "cost", "src", ""}, ""},
		{[]string{"1", "completion"}, "bash fish powershell zsh"},
		{[]string{"1", "demo"}, ""},
	} {
		if got := completeWords(t, tc.args...); got != tc.want {
			t.Errorf("__complete %q = %q, want %q", tc.args, got, tc.want)
		}
	}
	for _, bad := range [][]string{nil, {"x"}, {"-1"}, {"2", "set"}} {
		if err := run(append([]string{"__complete"}, bad...), nil, &bytes.Buffer{}); err == nil {
			t.Errorf("__complete %q should fail", bad)
		}
	}
}

// Every name and value completion offers is one set accepts.
func TestCompletedValuesAreAccepted(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	s, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	names := optionNames(nil)
	if len(names) != len(config.Options) {
		t.Fatalf("got %d names for %d options", len(names), len(config.Options))
	}
	for _, name := range names {
		for _, v := range optionValues(name) {
			if err := s.Set(name, v); err != nil {
				t.Errorf("completion offers %s %s: %v", name, v, err)
			}
		}
	}
}

func TestCompletionScripts(t *testing.T) {
	for _, shell := range shellNames() {
		var out bytes.Buffer
		if err := run([]string{"completion", shell}, nil, &out); err != nil {
			t.Fatal(err)
		}
		script := out.String()
		if !strings.Contains(script, "sill __complete") || !strings.Contains(script, dirsDirective) {
			t.Errorf("%s script does not ask sill __complete", shell)
		}
		if strings.Contains(script, "\r") {
			t.Errorf("%s script has a carriage return", shell)
		}
	}
	for _, bad := range [][]string{{"completion"}, {"completion", "cmd"}, {"completion", "bash", "zsh"}} {
		if err := run(bad, nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "powershell") {
			t.Errorf("%q: got %v", bad, err)
		}
	}
}
