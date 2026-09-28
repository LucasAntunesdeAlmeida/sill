// Package atomicfile replaces a file in one step: a reader sees the old content or the new,
// never a mix, and a crash mid-write leaves the old file in place.
package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Write puts data at path through a temporary file in the same directory and a rename. An
// existing file keeps its permissions, and a symlink keeps pointing where it did: the file
// it resolves to is the one replaced, so a settings.json linked from a dotfiles repository
// stays linked.
func Write(path string, data []byte, perm os.FileMode) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if fi, err := os.Stat(path); err == nil {
		perm = fi.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}
	if err := rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// rename retries briefly on Windows, where replacing a file fails while another process
// (Claude Code reading its settings, a virus scanner) has it open.
func rename(from, to string) error {
	err := os.Rename(from, to)
	if runtime.GOOS != "windows" {
		return err
	}
	for i := 0; err != nil && i < 10; i++ {
		time.Sleep(20 * time.Millisecond)
		err = os.Rename(from, to)
	}
	return err
}
