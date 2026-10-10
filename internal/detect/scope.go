package detect

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Tier says where a server's folder sits relative to the folder portlift was
// run from. Lower is closer, and closer wins.
type Tier int

const (
	TierBelow   Tier = iota // the current folder or below it
	TierAbove               // a folder above the current one, up to the repo root
	TierSibling             // elsewhere in the same repo
	TierOutside             // not in scope
)

// classify places dir relative to cwd and the repo root (empty when cwd is not
// in a repo). All three must already be canonical.
func classify(dir, cwd, root string) Tier {
	switch {
	case within(dir, cwd):
		return TierBelow
	case root != "" && within(cwd, dir) && within(dir, root):
		return TierAbove
	case root != "" && within(dir, root):
		return TierSibling
	}
	return TierOutside
}

var foldCase = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// within reports whether child is parent or a folder inside it. It compares
// whole path elements, so "app2" is not within "app".
func within(child, parent string) bool {
	if child == "" || parent == "" {
		return false
	}
	if foldCase {
		child, parent = strings.ToLower(child), strings.ToLower(parent)
	}
	if child == parent {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(parent, sep) {
		parent += sep
	}
	return strings.HasPrefix(child, parent)
}

// repoRoot returns the nearest folder at or above dir that holds a .git entry,
// or "" if there is none. Worktrees and submodules have a .git file rather
// than a folder, so either counts.
func repoRoot(dir string) string {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
