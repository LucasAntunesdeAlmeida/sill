// Package install points Claude Code's settings.json at the sill binary, and back.
package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Run writes the statusLine entry of dir/settings.json so it runs exe. Other settings and
// their order are preserved and the file is backed up before a change. Nothing is copied:
// the entry names the binary where it is, so a rebuild applies on the next render, and
// moving the binary means running install again. Progress is written to out.
func Run(dir, exe string, out io.Writer) error {
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	// Claude Code runs the command through Git Bash on Windows and sh elsewhere; both
	// want forward slashes and a quoted path.
	command := `"` + filepath.ToSlash(exe) + `"`

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	changed, err := edit(dir, func(raw []byte) ([]byte, bool, error) { return SetStatusLine(raw, command) })
	if err != nil {
		return err
	}
	if changed {
		fmt.Fprintf(out, "Updated statusLine in %s\n", filepath.Join(dir, "settings.json"))
	} else {
		fmt.Fprintln(out, "settings.json already points at this binary")
	}

	removeOldScripts(dir, out)
	pathHint(filepath.Dir(exe), os.Getenv("PATH"), out)
	fmt.Fprintln(out, "Restart Claude Code to pick up the status line.")
	return nil
}

// Uninstall removes the statusLine entry when it runs sill. Someone else's status line is
// left alone. The sill settings file is kept and mentioned.
func Uninstall(dir string, out io.Writer) error {
	changed, err := edit(dir, RemoveStatusLine)
	if err != nil {
		return err
	}
	if changed {
		fmt.Fprintf(out, "Removed statusLine from %s\n", filepath.Join(dir, "settings.json"))
	} else {
		fmt.Fprintln(out, "settings.json has no statusLine that runs sill, nothing to do")
	}
	if settings := filepath.Join(dir, "sill.json"); fileExists(settings) {
		fmt.Fprintf(out, "Your settings are still in %s, delete it if you are done with sill.\n", settings)
	}
	fmt.Fprintln(out, "Restart Claude Code to apply.")
	return nil
}

// BackupName is the copy of settings.json taken before a change. One file, overwritten
// each time, so repeated installs do not litter the config directory.
const BackupName = "settings.json.sill.bak"

// edit reads settings.json, applies fn, and writes the result after saving the previous
// content to BackupName when fn reports a change. A missing file is treated as empty.
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
		if err := os.WriteFile(filepath.Join(dir, BackupName), raw, 0o644); err != nil {
			return false, err
		}
	}
	return true, os.WriteFile(path, updated, 0o644)
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
	base := filepath.Base(strings.Trim(strings.TrimSpace(cmd), `"`))
	if base != "sill" && base != "sill.exe" {
		return raw, false, nil
	}
	entries = append(entries[:idx], entries[idx+1:]...)
	out, err := formatEntries(entries)
	return out, true, err
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
