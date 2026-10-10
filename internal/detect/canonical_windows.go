package detect

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// canonical resolves p to the path Windows itself reports for the folder, so
// that junctions, symlinks, mapped drives and 8.3 names all compare equal.
func canonical(p string) string {
	p = filepath.Clean(p)
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return p
	}
	// FILE_FLAG_BACKUP_SEMANTICS is what allows opening a directory.
	h, err := windows.CreateFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return p
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return p
		}
		if int(n) < len(buf) {
			break
		}
		buf = make([]uint16, n+1)
	}
	final := windows.UTF16ToString(buf)
	if rest, ok := strings.CutPrefix(final, `\\?\UNC\`); ok {
		return filepath.Clean(`\\` + rest)
	}
	return filepath.Clean(strings.TrimPrefix(final, `\\?\`))
}
