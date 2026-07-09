package eql

import (
	"os"
	"strings"
	"testing"
)

// isLFSPointer reports whether the file at path is an unresolved Git LFS
// pointer (a small text stub) rather than its real content. This happens when
// a checkout does not materialize LFS objects (e.g. CI without `lfs: true`).
func isLFSPointer(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 64)
	n, _ := f.Read(buf)
	return strings.HasPrefix(string(buf[:n]), "version https://git-lfs")
}

// skipIfLFSPointer skips the test when the corpus file is an unresolved LFS
// pointer, so corpus-backed tests degrade to a skip (not a failure) on
// checkouts without LFS content.
func skipIfLFSPointer(t testing.TB, path string) {
	t.Helper()
	if isLFSPointer(path) {
		t.Skipf("%s is an unresolved Git LFS pointer (checkout without LFS content)", path)
	}
}
