package gitinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDirStatus(t *testing.T) {
	dir := t.TempDir()
	if got := DirStatus(dir); got != "" {
		t.Errorf("clean dir = %q", got)
	}
	cases := []struct {
		marker string
		isDir  bool
		want   string
	}{
		{"MERGE_HEAD", false, "MERGING"},
		{"rebase-merge", true, "REBASING"},
		{"rebase-apply", true, "REBASING"},
		{"CHERRY_PICK_HEAD", false, "CHERRY-PICKING"},
		{"REVERT_HEAD", false, "REVERTING"},
		{"BISECT_LOG", false, "BISECTING"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		path := filepath.Join(dir, c.marker)
		var err error
		if c.isDir {
			err = os.Mkdir(path, 0o755)
		} else {
			err = os.WriteFile(path, nil, 0o644)
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := DirStatus(dir); got != c.want {
			t.Errorf("%s: got %q, want %q", c.marker, got, c.want)
		}
	}
}

func TestLookup(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if got := Lookup("", false); got != (State{}) {
		t.Errorf("empty cwd = %+v", got)
	}
	plain := t.TempDir()
	if got := Lookup(plain, true); got != (State{}) {
		t.Errorf("outside a repo = %+v", got)
	}

	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	if got := Lookup(repo, false); got.Branch != "main" || got.Status != "" {
		t.Errorf("unborn branch = %+v", got)
	}
	file := filepath.Join(repo, "a.txt")
	if err := os.WriteFile(file, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "a.txt")
	git("commit", "-q", "-m", "one")
	if got := Lookup(repo, true); got.Branch != "main" || got.Dirty {
		t.Errorf("clean checkout = %+v", got)
	}

	// Untracked files do not count, tracked changes do, and dirty is only computed on request.
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Lookup(repo, true); got.Dirty {
		t.Errorf("untracked file counted as dirty: %+v", got)
	}
	if err := os.WriteFile(file, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Lookup(repo, true); !got.Dirty {
		t.Errorf("modified file not dirty: %+v", got)
	}
	if got := Lookup(repo, false); got.Dirty {
		t.Errorf("dirty computed without being asked: %+v", got)
	}

	git("checkout", "-q", "--detach")
	if got := Lookup(repo, false); len(got.Branch) != 7 || got.Branch == "HEAD" {
		t.Errorf("detached = %+v", got)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "MERGE_HEAD"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Lookup(repo, false); got.Status != "MERGING" {
		t.Errorf("merging = %+v", got)
	}
}

func TestSplitLines(t *testing.T) {
	got := splitLines(".git\r\nabc\r\nmain\r\n")
	if len(got) != 3 || got[0] != ".git" || got[2] != "main" {
		t.Errorf("crlf = %q", got)
	}
	if got := splitLines(""); got != nil {
		t.Errorf("empty = %q", got)
	}
}
