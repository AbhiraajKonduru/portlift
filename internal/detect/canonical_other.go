//go:build !windows

package detect

import "path/filepath"

// canonical resolves symlinks in p so that two routes to one folder compare
// equal.
func canonical(p string) string {
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}
