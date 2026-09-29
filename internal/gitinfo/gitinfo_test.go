package gitinfo

import (
	"context"
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
	if got := Lookup(context.Background(), "", false); got != (State{}) {
		t.Errorf("empty cwd = %+v", got)
	}
	plain := t.TempDir()
	if got := Lookup(context.Background(), plain, true); got != (State{}) {
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
	if got := Lookup(context.Background(), repo, false); got.Branch != "main" || got.Status != "" {
		t.Errorf("unborn branch = %+v", got)
	}
	// Reading HEAD must agree with asking git, wherever in the tree we are.
	sameAsGit := func(what, dir string) {
		t.Helper()
		fast, ok := ReadHead(dir)
		if !ok {
			t.Errorf("%s: ReadHead fell back to git", what)
		}
		if slow := lookupGit(context.Background(), dir); fast != slow {
			t.Errorf("%s: ReadHead %+v, git %+v", what, fast, slow)
		}
	}
	sameAsGit("unborn", repo)
	if err := os.MkdirAll(filepath.Join(repo, "sub", "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	sameAsGit("subfolder", filepath.Join(repo, "sub", "dir"))
	file := filepath.Join(repo, "a.txt")
	if err := os.WriteFile(file, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "a.txt")
	git("commit", "-q", "-m", "one")
	if got := Lookup(context.Background(), repo, true); got.Branch != "main" || got.Dirty {
		t.Errorf("clean checkout = %+v", got)
	}
	sameAsGit("branch", repo)
	git("checkout", "-q", "-b", "feature/billing")
	sameAsGit("branch with a slash", repo)

	// A linked worktree has a .git file pointing into the main repository.
	wt := filepath.Join(t.TempDir(), "wt")
	git("worktree", "add", "-q", "-b", "spike", wt)
	sameAsGit("linked worktree", wt)
	if got := Lookup(context.Background(), wt, false); got.Branch != "spike" {
		t.Errorf("worktree = %+v", got)
	}
	git("checkout", "-q", "main")

	// Untracked files do not count, tracked changes do, and dirty is only computed on request.
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Lookup(context.Background(), repo, true); got.Dirty {
		t.Errorf("untracked file counted as dirty: %+v", got)
	}
	if err := os.WriteFile(file, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Lookup(context.Background(), repo, true); !got.Dirty {
		t.Errorf("modified file not dirty: %+v", got)
	}
	if got := Lookup(context.Background(), repo, false); got.Dirty {
		t.Errorf("dirty computed without being asked: %+v", got)
	}

	git("checkout", "-q", "--detach")
	if got := Lookup(context.Background(), repo, false); len(got.Branch) != 7 || got.Branch == "HEAD" {
		t.Errorf("detached = %+v", got)
	}
	sameAsGit("detached", repo)
	if err := os.WriteFile(filepath.Join(repo, ".git", "MERGE_HEAD"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Lookup(context.Background(), repo, false); got.Status != "MERGING" {
		t.Errorf("merging = %+v", got)
	}
	sameAsGit("merging", repo)

	// With the render's budget spent, git is not waited for; the HEAD file still answers.
	done, cancel := context.WithCancel(context.Background())
	cancel()
	if got := Lookup(done, repo, true); got.Branch == "" || got.Dirty {
		t.Errorf("expired budget, HEAD readable = %+v", got)
	}
	t.Setenv("GIT_DIR", filepath.Join(repo, ".git"))
	if got := Lookup(done, repo, false); got != (State{}) {
		t.Errorf("expired budget, git needed = %+v", got)
	}
}

// ReadHead on hand-made repositories, no git needed.
func TestReadHead(t *testing.T) {
	t.Setenv("GIT_DIR", "")
	t.Setenv("GIT_WORK_TREE", "")
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	write(filepath.Join(root, "repo", ".git", "HEAD"), "ref: refs/heads/feature/x\n")
	write(filepath.Join(root, "repo", "deep", "er", "file"), "")
	if got, ok := ReadHead(filepath.Join(root, "repo", "deep", "er")); !ok || got.Branch != "feature/x" {
		t.Errorf("branch = %+v, %v", got, ok)
	}

	write(filepath.Join(root, "detached", ".git", "HEAD"), "0123456789abcdef0123456789abcdef01234567\n")
	write(filepath.Join(root, "detached", ".git", "rebase-merge", "x"), "")
	if got, ok := ReadHead(filepath.Join(root, "detached")); !ok || got.Branch != "0123456" || got.Status != "REBASING" {
		t.Errorf("detached = %+v, %v", got, ok)
	}

	// A worktree or submodule: .git is a file with a relative gitdir.
	write(filepath.Join(root, "main", ".git", "worktrees", "wt", "HEAD"), "ref: refs/heads/spike\n")
	write(filepath.Join(root, "wt", ".git"), "gitdir: ../main/.git/worktrees/wt\n")
	if got, ok := ReadHead(filepath.Join(root, "wt")); !ok || got.Branch != "spike" {
		t.Errorf("worktree = %+v, %v", got, ok)
	}

	// Cases git has to answer.
	fallbacks := map[string]string{
		"reftable":  "ref: refs/heads/.invalid\n",
		"other ref": "ref: refs/remotes/origin/main\n",
		"garbage":   "not a head\n",
	}
	for name, head := range fallbacks {
		write(filepath.Join(root, name, ".git", "HEAD"), head)
		if _, ok := ReadHead(filepath.Join(root, name)); ok {
			t.Errorf("%s: should fall back to git", name)
		}
	}
	write(filepath.Join(root, "badlink", ".git"), "not a gitdir line")
	if _, ok := ReadHead(filepath.Join(root, "badlink")); ok {
		t.Error("unreadable .git file should fall back to git")
	}
	t.Setenv("GIT_DIR", filepath.Join(root, "repo", ".git"))
	if _, ok := ReadHead(filepath.Join(root, "repo")); ok {
		t.Error("GIT_DIR should fall back to git")
	}
}

func TestRoot(t *testing.T) {
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	main := filepath.Join(root, "main")
	write(filepath.Join(main, ".git", "HEAD"), "ref: refs/heads/main\n")
	write(filepath.Join(main, "src", "x"), "")
	// A linked worktree elsewhere, the way git writes it.
	write(filepath.Join(main, ".git", "worktrees", "wt", "commondir"), "../..\n")
	write(filepath.Join(root, "wt", ".git"), "gitdir: "+filepath.Join(main, ".git", "worktrees", "wt")+"\n")
	write(filepath.Join(root, "wt", "sub", "x"), "")
	// A submodule: its git directory lives in the parent's, without a commondir.
	write(filepath.Join(main, ".git", "modules", "lib", "HEAD"), "ref: refs/heads/main\n")
	write(filepath.Join(main, "lib", ".git"), "gitdir: ../.git/modules/lib\n")
	// Broken .git file.
	write(filepath.Join(root, "broken", ".git"), "nonsense")
	plain := filepath.Join(root, "plain")
	write(filepath.Join(plain, "x"), "")

	cases := map[string]string{
		filepath.Join(main, "src"):       main,
		main:                             main,
		filepath.Join(root, "wt", "sub"): main,
		filepath.Join(main, "lib"):       filepath.Join(main, "lib"),
		filepath.Join(root, "broken"):    filepath.Join(root, "broken"),
	}
	for cwd, want := range cases {
		if got := Root(cwd); got != want {
			t.Errorf("Root(%s) = %s, want %s", cwd, got, want)
		}
	}
	// Outside a repository the folder itself, unless the temp dir sits inside one.
	if _, _, found, _ := findGitDir(plain); !found {
		if got := Root(plain); got != plain {
			t.Errorf("Root(plain) = %s", got)
		}
	}

	if _, err := exec.LookPath("git"); err != nil {
		return
	}
	repo := filepath.Join(t.TempDir(), "repo")
	linked := filepath.Join(filepath.Dir(repo), "linked")
	for _, args := range [][]string{
		{"init", "-q", repo},
		{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "x"},
		{"-C", repo, "worktree", "add", "-q", linked},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if got := Root(linked); !sameDir(got, repo) {
		t.Errorf("real worktree: Root = %s, want %s", got, repo)
	}
}

// sameDir compares folders that may be spelled differently, such as a short Windows name.
func sameDir(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

func TestIsHash(t *testing.T) {
	for s, want := range map[string]bool{
		"0123456789abcdef0123456789abcdef01234567":                         true,
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef": true,
		"0123456789ABCDEF0123456789abcdef01234567":                         false,
		"0123456": false,
		"":        false,
	} {
		if got := isHash(s); got != want {
			t.Errorf("isHash(%q) = %v", s, got)
		}
	}
}

func BenchmarkReadHead(b *testing.B) {
	dir := b.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	cwd := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if st, _ := ReadHead(cwd); st.Branch != "main" {
			b.Fatal(st)
		}
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
