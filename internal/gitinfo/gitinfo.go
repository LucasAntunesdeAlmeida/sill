// Package gitinfo reports the branch and any in-progress operation of a repository with a
// single git call per render, plus an optional second call for uncommitted changes.
package gitinfo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// State is what the line shows: the branch (or short sha when detached), an operation
// such as MERGING, and whether the tree has uncommitted changes when that was asked for.
type State struct {
	Branch string
	Status string
	Dirty  bool
}

// Timeout bounds each git call so a hung filesystem cannot stall the status line.
const Timeout = 3 * time.Second

// Lookup spawns git once. One rev-parse returns the git dir, the full sha and the branch
// name ("HEAD" when detached); --abbrev-ref sticks for later args and --short cannot be
// combined, hence the order. Outside a repo it prints nothing; on an unborn branch it
// prints the git dir then fails, and a second call gets the branch name.
//
// With dirty set, a second call runs git status without touching untracked files or the
// index lock. Without it no git status is run, so large repos stay fast.
func Lookup(cwd string, dirty bool) State {
	if cwd == "" {
		return State{}
	}
	if _, err := exec.LookPath("git"); err != nil {
		return State{}
	}
	out, err := run(cwd, "rev-parse", "--git-dir", "HEAD", "--abbrev-ref", "HEAD")
	lines := splitLines(out)
	if len(lines) == 0 || lines[0] == "" {
		return State{}
	}
	gitDir := lines[0]
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(cwd, gitDir)
	}

	var branch string
	if err == nil && len(lines) >= 3 {
		branch = lines[2]
		if branch == "HEAD" && len(lines[1]) >= 7 {
			branch = lines[1][:7]
		}
	} else {
		out, _ := run(cwd, "symbolic-ref", "--short", "HEAD")
		branch = strings.TrimSpace(out)
	}
	st := State{Branch: branch, Status: DirStatus(gitDir)}
	if dirty {
		st.Dirty = isDirty(cwd)
	}
	return st
}

// DirStatus reads the marker files git leaves in its directory while an operation is in
// progress.
func DirStatus(gitDir string) string {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(gitDir, name))
		return err == nil
	}
	switch {
	case exists("MERGE_HEAD"):
		return "MERGING"
	case exists("rebase-merge"), exists("rebase-apply"):
		return "REBASING"
	case exists("CHERRY_PICK_HEAD"):
		return "CHERRY-PICKING"
	case exists("REVERT_HEAD"):
		return "REVERTING"
	case exists("BISECT_LOG"):
		return "BISECTING"
	}
	return ""
}

// isDirty reports tracked changes, staged or not. Untracked files are ignored, and
// --no-optional-locks keeps a background render from writing the index.
func isDirty(cwd string) bool {
	out, err := run(cwd, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
	return err == nil && strings.TrimSpace(out) != ""
}

func run(cwd string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...)
	hideWindow(cmd)
	out, err := cmd.Output()
	return string(out), err
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\r\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	return lines
}
