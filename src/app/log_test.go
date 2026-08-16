package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDiagLogWrites0600 pins the file log (F5): the cache-dir log
// receives the entry, and the file is 0600 - the default handler
// discards, so the marker only lands after openDiagLog.
func TestDiagLogWrites0600(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	openDiagLog()
	diag.Info("test marker")
	path := filepath.Join(os.Getenv("XDG_CACHE_HOME"), "notmutt", "notmutt.log")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "test marker") {
		t.Fatalf("log entry missing: %q", b)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("log perms = %v, want 0600", st.Mode().Perm())
	}
}
