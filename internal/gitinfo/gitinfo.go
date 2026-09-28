// Package gitinfo reports the branch and any in-progress operation of a repository. The
// branch is read from the repository's HEAD file; git itself runs only when that file
// cannot answer, and for the optional check for uncommitted changes.
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

// Timeout bounds each git call so a hung filesystem cannot stall the status line. The
// caller's context usually ends sooner: it is the budget of the whole render.
const Timeout = 3 * time.Second

// Lookup finds the repository around cwd and reports its state. Reading HEAD answers in
// well under a millisecond where starting git costs tens of them (most on Windows), so git
// only runs when the files cannot answer: GIT_DIR or GIT_WORK_TREE set, a reftable
// repository, or a HEAD sill does not recognise.
//
// With dirty set, git status runs as well, without touching untracked files or the index
// lock. Without it no git status is run, so large repos stay fast.
func Lookup(ctx context.Context, cwd string, dirty bool) State {
	if cwd == "" {
		return State{}
	}
	st, ok := ReadHead(cwd)
	if !ok {
		st = lookupGit(ctx, cwd)
	}
	if dirty && st.Branch != "" {
		st.Dirty = isDirty(ctx, cwd)
	}
	return st
}

// ReadHead answers from the repository files alone. ok is false when git has to be asked;
// a folder that is in no repository is a definite answer, an empty State with ok true.
func ReadHead(cwd string) (State, bool) {
	if os.Getenv("GIT_DIR") != "" || os.Getenv("GIT_WORK_TREE") != "" {
		return State{}, false
	}
	gitDir, found, ok := findGitDir(cwd)
	if !ok {
		return State{}, false
	}
	if !found {
		return State{}, true
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return State{}, false
	}
	head := strings.TrimSpace(string(data))
	var branch string
	if ref, isRef := strings.CutPrefix(head, "ref: "); isRef {
		name, isBranch := strings.CutPrefix(ref, "refs/heads/")
		// A reftable repository keeps a placeholder here and the real HEAD elsewhere.
		if !isBranch || name == "" || name == ".invalid" {
			return State{}, false
		}
		branch = name
	} else if isHash(head) {
		branch = head[:7]
	} else {
		return State{}, false
	}
	return State{Branch: branch, Status: DirStatus(gitDir)}, true
}

// findGitDir walks up from cwd to the first .git entry: a directory, or in a linked
// worktree or submodule a file naming the directory. found is false when there is none up
// to the root; ok is false when a .git entry exists but cannot be read.
func findGitDir(cwd string) (dir string, found, ok bool) {
	for d := filepath.Clean(cwd); ; {
		dotGit := filepath.Join(d, ".git")
		fi, err := os.Stat(dotGit)
		if err == nil {
			if fi.IsDir() {
				return dotGit, true, true
			}
			data, err := os.ReadFile(dotGit)
			if err != nil {
				return "", true, false
			}
			target, isLink := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
			if !isLink || target == "" {
				return "", true, false
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(d, target)
			}
			return target, true, true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", false, true
		}
		d = parent
	}
}

// isHash reports a full SHA-1 or SHA-256 object name.
func isHash(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	return strings.Trim(s, "0123456789abcdef") == ""
}

// lookupGit spawns git once. One rev-parse returns the git dir, the full sha and the branch
// name ("HEAD" when detached); --abbrev-ref sticks for later args and --short cannot be
// combined, hence the order. Outside a repo it prints nothing; on an unborn branch it
// prints the git dir then fails, and a second call gets the branch name.
func lookupGit(ctx context.Context, cwd string) State {
	if _, err := exec.LookPath("git"); err != nil {
		return State{}
	}
	out, err := run(ctx, cwd, "rev-parse", "--git-dir", "HEAD", "--abbrev-ref", "HEAD")
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
		out, _ := run(ctx, cwd, "symbolic-ref", "--short", "HEAD")
		branch = strings.TrimSpace(out)
	}
	return State{Branch: branch, Status: DirStatus(gitDir)}
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
func isDirty(ctx context.Context, cwd string) bool {
	out, err := run(ctx, cwd, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
	return err == nil && strings.TrimSpace(out) != ""
}

func run(ctx context.Context, cwd string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
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
