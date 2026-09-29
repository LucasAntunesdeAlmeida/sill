// Package install points Claude Code's settings.json at the sill binary, and back.
package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/LucasAntunesdeAlmeida/sill/internal/atomicfile"
)

// Run writes the statusLine entry of dir/settings.json so it runs exe, and a SessionEnd
// hook that records what each session cost. Other settings, hooks and their order are
// preserved and the file is backed up before a change. Nothing is copied: the entries name
// the binary where it is, so a rebuild applies on the next render, and moving the binary
// means running install again. Progress is written to out.
func Run(dir, exe string, out io.Writer) error {
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if IsTempBuild(exe) {
		return fmt.Errorf("%s is a temporary `go run` build that is deleted when it exits;\n"+
			"run `go install ./cmd/sill` (or build a binary) and then `sill install`", exe)
	}
	// Claude Code runs the command through Git Bash on Windows and sh elsewhere; both
	// want forward slashes and a quoted path.
	command := `"` + filepath.ToSlash(exe) + `"`

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var lineChanged, hookChanged bool
	var hookErr error
	_, err := edit(dir, func(raw []byte) ([]byte, bool, error) {
		updated, changed, err := SetStatusLine(raw, command)
		if err != nil {
			return nil, false, err
		}
		lineChanged = changed
		// The hook is a bonus: settings whose hooks sill cannot edit still get the line.
		withHook, changed, err := SetHook(updated, command+" "+hookArgs)
		if err != nil {
			hookErr = err
			return updated, lineChanged, nil
		}
		hookChanged = changed
		return withHook, lineChanged || hookChanged, nil
	})
	if err != nil {
		return err
	}
	if lineChanged {
		fmt.Fprintf(out, "Updated statusLine in %s\n", filepath.Join(dir, "settings.json"))
	} else {
		fmt.Fprintln(out, "settings.json already points at this binary")
	}
	switch {
	case hookErr != nil:
		fmt.Fprintf(out, "Could not add the %s hook that records session costs: %v\n", HookEvent, hookErr)
	case hookChanged:
		fmt.Fprintf(out, "Added a %s hook that records what each session cost (see `sill cost`)\n", HookEvent)
	}

	removeOldScripts(dir, out)
	pathHint(filepath.Dir(exe), os.Getenv("PATH"), out)
	fmt.Fprintln(out, "Restart Claude Code to pick up the status line.")
	return nil
}

// Uninstall removes the statusLine entry when it runs sill, and sill's hook. Someone
// else's status line and hooks are left alone. The sill settings file and the cost ledger
// are kept and mentioned.
func Uninstall(dir string, out io.Writer) error {
	var lineChanged, hookChanged bool
	_, err := edit(dir, func(raw []byte) ([]byte, bool, error) {
		updated, changed, err := RemoveStatusLine(raw)
		if err != nil {
			return nil, false, err
		}
		lineChanged = changed
		if withoutHook, changed, err := RemoveHook(updated); err == nil {
			updated, hookChanged = withoutHook, changed
		}
		return updated, lineChanged || hookChanged, nil
	})
	if err != nil {
		return err
	}
	if lineChanged {
		fmt.Fprintf(out, "Removed statusLine from %s\n", filepath.Join(dir, "settings.json"))
	}
	if hookChanged {
		fmt.Fprintf(out, "Removed the %s hook\n", HookEvent)
	}
	if !lineChanged && !hookChanged {
		fmt.Fprintln(out, "settings.json has no statusLine or hook that runs sill, nothing to do")
	}
	for _, name := range []string{"sill.json", "sill-costs"} {
		if p := filepath.Join(dir, name); fileExists(p) {
			fmt.Fprintf(out, "%s is still there, delete it if you are done with sill.\n", p)
		}
	}
	fmt.Fprintln(out, "Restart Claude Code to apply.")
	return nil
}

// BackupName is the copy of settings.json taken before a change. One file, overwritten
// each time, so repeated installs do not litter the config directory.
const BackupName = "settings.json.sill.bak"

// IsTempBuild reports whether exe lives in a folder `go run` creates and removes, such as
// $TMPDIR/go-build1234/b001/exe/sill.
func IsTempBuild(exe string) bool {
	for part := range strings.SplitSeq(strings.ReplaceAll(exe, `\`, "/"), "/") {
		digits, ok := strings.CutPrefix(part, "go-build")
		if ok && digits != "" && strings.Trim(digits, "0123456789") == "" {
			return true
		}
	}
	return false
}

// Current returns the statusLine command in dir/settings.json, or "" when there is none.
func Current(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	entries, err := parseEntries(raw)
	if err != nil {
		return "", fmt.Errorf("settings.json: %w", err)
	}
	_, current := findStatusLine(entries)
	var cmd string
	_ = json.Unmarshal(current["command"], &cmd)
	return cmd, nil
}

// CommandPath is the program a statusLine command runs: the quoted path sill install
// writes, or the first word of a command written by hand.
func CommandPath(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if rest, ok := strings.CutPrefix(cmd, `"`); ok {
		path, _, _ := strings.Cut(rest, `"`)
		return path
	}
	path, _, _ := strings.Cut(cmd, " ")
	return path
}

// IsSill reports whether a statusLine command runs sill, under its own name or a release
// asset's (sill-windows-amd64.exe) or one ending in a version.
func IsSill(cmd string) bool {
	name := strings.ToLower(path.Base(strings.ReplaceAll(CommandPath(cmd), `\`, "/")))
	name = strings.TrimSuffix(name, ".exe")
	return name == "sill" || strings.HasPrefix(name, "sill-") || strings.HasPrefix(name, "sill_")
}

// edit reads settings.json, applies fn, and writes the result after saving the previous
// content to BackupName when fn reports a change. A missing file is treated as empty. The
// file is replaced in one step, so an interrupted install cannot leave it half written.
func edit(dir string, fn func(raw []byte) ([]byte, bool, error)) (bool, error) {
	path := filepath.Join(dir, "settings.json")
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	updated, changed, err := fn(raw)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if !changed {
		return false, nil
	}
	if raw != nil {
		if err := atomicfile.Write(filepath.Join(dir, BackupName), raw, 0o644); err != nil {
			return false, err
		}
	}
	return true, atomicfile.Write(path, updated, 0o644)
}

type entry struct {
	key string
	val json.RawMessage
}

// parseEntries decodes the top-level keys of a settings document in order.
func parseEntries(raw []byte) ([]entry, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if tok != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	var entries []entry
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := kt.(string)
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, err
		}
		entries = append(entries, entry{key, val})
	}
	// The closing brace, then nothing: a truncated file or one with trailing data is not
	// rewritten as if it were whole.
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, errors.New("unterminated JSON object")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the JSON object")
	}
	return entries, nil
}

// formatEntries writes the document back with two-space indentation.
func formatEntries(entries []entry) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, e := range entries {
		key, _ := json.Marshal(e.key)
		b.WriteString("  ")
		b.Write(key)
		b.WriteString(": ")
		var v bytes.Buffer
		if err := json.Indent(&v, e.val, "  ", "  "); err != nil {
			return nil, err
		}
		b.Write(v.Bytes())
		if i < len(entries)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.Bytes(), nil
}

func findStatusLine(entries []entry) (int, map[string]json.RawMessage) {
	current := map[string]json.RawMessage{}
	for i, e := range entries {
		if e.key == "statusLine" {
			_ = json.Unmarshal(e.val, &current)
			return i, current
		}
	}
	return -1, current
}

// SetStatusLine rewrites the statusLine entry of a settings.json document, keeping every
// other key in place and in order. It reports false when the entry already matches.
func SetStatusLine(raw []byte, command string) ([]byte, bool, error) {
	entries, err := parseEntries(raw)
	if err != nil {
		return nil, false, err
	}
	// Keep any extra statusLine keys (padding, refreshInterval) the user may have set.
	idx, current := findStatusLine(entries)
	var curType, curCmd string
	_ = json.Unmarshal(current["type"], &curType)
	_ = json.Unmarshal(current["command"], &curCmd)
	if curType == "command" && curCmd == command {
		return raw, false, nil
	}
	current["type"] = json.RawMessage(`"command"`)
	cmdJSON, _ := json.Marshal(command)
	current["command"] = cmdJSON
	val, _ := json.Marshal(current)
	if idx >= 0 {
		entries[idx].val = val
	} else {
		entries = append(entries, entry{"statusLine", val})
	}
	out, err := formatEntries(entries)
	return out, true, err
}

// RemoveStatusLine drops the statusLine entry when its command runs sill. It reports
// false when there is nothing to remove or the entry belongs to something else.
func RemoveStatusLine(raw []byte) ([]byte, bool, error) {
	entries, err := parseEntries(raw)
	if err != nil {
		return nil, false, err
	}
	idx, current := findStatusLine(entries)
	if idx < 0 {
		return raw, false, nil
	}
	var cmd string
	_ = json.Unmarshal(current["command"], &cmd)
	if !IsSill(cmd) {
		return raw, false, nil
	}
	entries = append(entries[:idx], entries[idx+1:]...)
	out, err := formatEntries(entries)
	return out, true, err
}

// HookEvent is when sill records what a session cost: as Claude Code ends it.
const HookEvent = "SessionEnd"

// hookArgs is what sill is run with as a hook.
const hookArgs = "hook"

// IsSillHook reports whether a hook command runs sill's hook.
func IsSillHook(cmd string) bool {
	if !IsSill(cmd) {
		return false
	}
	cmd = strings.TrimSpace(cmd)
	var args string
	if rest, ok := strings.CutPrefix(cmd, `"`); ok {
		_, args, _ = strings.Cut(rest, `"`)
	} else {
		_, args, _ = strings.Cut(cmd, " ")
	}
	return strings.TrimSpace(args) == hookArgs
}

// HookCommands returns the sill hook commands registered for HookEvent in dir/settings.json.
func HookCommands(dir string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_, groups, _, err := hookGroups(raw)
	if err != nil {
		return nil, fmt.Errorf("settings.json: %w", err)
	}
	_, found := stripSillHooks(groups)
	return found, nil
}

// SetHook registers command as a HookEvent hook, replacing any other sill hook there and
// leaving everyone else's hooks, and their order, alone. It reports false when command is
// already registered.
func SetHook(raw []byte, command string) ([]byte, bool, error) {
	entries, groups, hooks, err := hookGroups(raw)
	if err != nil {
		return nil, false, err
	}
	kept, found := stripSillHooks(groups)
	if len(found) == 1 && found[0] == command && len(kept) == len(groups)-1 {
		return raw, false, nil
	}
	ours, _ := json.Marshal(map[string]any{
		"hooks": []map[string]string{{"type": "command", "command": command}},
	})
	out, err := putHookGroups(entries, hooks, append(kept, ours))
	return out, true, err
}

// RemoveHook drops every sill hook from HookEvent, and the event or the hooks setting
// when that leaves them empty. It reports false when there was none.
func RemoveHook(raw []byte) ([]byte, bool, error) {
	entries, groups, hooks, err := hookGroups(raw)
	if err != nil {
		return nil, false, err
	}
	kept, found := stripSillHooks(groups)
	if len(found) == 0 {
		return raw, false, nil
	}
	out, err := putHookGroups(entries, hooks, kept)
	return out, true, err
}

// hookGroups decodes a settings document down to the matcher groups of HookEvent.
func hookGroups(raw []byte) (entries []entry, groups []json.RawMessage, hooks []entry, err error) {
	entries, err = parseEntries(raw)
	if err != nil {
		return nil, nil, nil, err
	}
	if i := indexOf(entries, "hooks"); i >= 0 {
		if hooks, err = parseEntries(entries[i].val); err != nil {
			return nil, nil, nil, fmt.Errorf("hooks: %w", err)
		}
	}
	if i := indexOf(hooks, HookEvent); i >= 0 {
		if err := json.Unmarshal(hooks[i].val, &groups); err != nil {
			return nil, nil, nil, fmt.Errorf("hooks.%s is not a list", HookEvent)
		}
	}
	return entries, groups, hooks, nil
}

// putHookGroups writes groups back as HookEvent's hooks, dropping what ends up empty.
func putHookGroups(entries []entry, hooks []entry, groups []json.RawMessage) ([]byte, error) {
	hooks = setEntry(hooks, HookEvent, groups == nil, func() json.RawMessage {
		val, _ := json.Marshal(groups)
		return val
	})
	entries = setEntry(entries, "hooks", len(hooks) == 0, func() json.RawMessage { return compact(hooks) })
	return formatEntries(entries)
}

// setEntry replaces the value of key, appends it when missing, or with remove drops it.
func setEntry(entries []entry, key string, remove bool, val func() json.RawMessage) []entry {
	i := indexOf(entries, key)
	switch {
	case remove && i >= 0:
		return append(entries[:i], entries[i+1:]...)
	case remove:
		return entries
	case i >= 0:
		entries[i].val = val()
		return entries
	}
	return append(entries, entry{key, val()})
}

// stripSillHooks removes sill's hook commands from matcher groups, and groups left with
// no hooks. Groups without one are kept byte for byte. found lists the commands removed.
func stripSillHooks(groups []json.RawMessage) (kept []json.RawMessage, found []string) {
	for _, g := range groups {
		var group map[string]json.RawMessage
		var hooks []json.RawMessage
		if json.Unmarshal(g, &group) != nil || json.Unmarshal(group["hooks"], &hooks) != nil {
			kept = append(kept, g)
			continue
		}
		var rest []json.RawMessage
		for _, h := range hooks {
			var hook struct{ Command string }
			if json.Unmarshal(h, &hook) == nil && IsSillHook(hook.Command) {
				found = append(found, hook.Command)
				continue
			}
			rest = append(rest, h)
		}
		switch {
		case len(rest) == len(hooks):
			kept = append(kept, g)
		case len(rest) > 0:
			group["hooks"], _ = json.Marshal(rest)
			val, _ := json.Marshal(group)
			kept = append(kept, val)
		}
	}
	return kept, found
}

func indexOf(entries []entry, key string) int {
	for i, e := range entries {
		if e.key == key {
			return i
		}
	}
	return -1
}

// compact writes entries as one JSON object in their order; formatEntries indents it.
func compact(entries []entry) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, e := range entries {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(e.key)
		b.Write(key)
		b.WriteByte(':')
		b.Write(e.val)
	}
	b.WriteByte('}')
	return b.Bytes()
}

// removeOldScripts clears the symlinks the shell-script predecessor installed. Real files
// are left alone and reported.
func removeOldScripts(dir string, out io.Writer) {
	for _, name := range []string{"statusline-command.ps1", "statusline-command.sh"} {
		path := filepath.Join(dir, name)
		fi, err := os.Lstat(path)
		if err != nil {
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(path); err == nil {
				fmt.Fprintf(out, "Removed old link %s\n", path)
			}
			continue
		}
		fmt.Fprintf(out, "Note: %s is no longer used and can be deleted\n", path)
	}
}

// pathHint suggests adding the binary's folder to PATH so `sill set` works from anywhere.
func pathHint(dir, pathEnv string, out io.Writer) {
	dir = filepath.Clean(dir)
	for _, p := range filepath.SplitList(pathEnv) {
		p = filepath.Clean(p)
		if p == dir || (runtime.GOOS == "windows" && strings.EqualFold(p, dir)) {
			return
		}
	}
	fmt.Fprintf(out, "Tip: add %s to PATH to run `sill set ...` from anywhere,\n", dir)
	fmt.Fprintln(out, "     including inside Claude Code as `! sill set layout full`.")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
