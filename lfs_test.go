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

func TestLFSPointerDetection(t *testing.T) {
	if isLFSPointer("does-not-exist-anywhere") {
		t.Error("missing file must not be treated as a pointer")
	}
	if isLFSPointer("go.mod") {
		t.Error("a real file must not be treated as a pointer")
	}
	ptr := t.TempDir() + "/ptr"
	if err := os.WriteFile(ptr, []byte("version https://git-lfs.github.com/spec/v1\noid sha256:x\nsize 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isLFSPointer(ptr) {
		t.Error("pointer stub not detected")
	}
	// The skip path: this subtest must be skipped, never reaching the error.
	t.Run("skip-path", func(t *testing.T) {
		skipIfLFSPointer(t, ptr)
		t.Error("skipIfLFSPointer should have skipped on a pointer file")
	})
}
