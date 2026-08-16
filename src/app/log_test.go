package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDiagLogRotates pins the size cap: writes past the cap rotate to
// <name>.1 (one generation kept) and the live file stays at or under
// the cap with 0600 perms (F5) - the rename-failure path truncates.
func TestDiagLogRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notmutt.log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	c := &cappedFile{f: f, path: path, cap: 64}
	blob := []byte(strings.Repeat("x", 32) + "\n")
	for i := 0; i < 4; i++ {
		if _, err := c.Write(blob); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatal("rotated generation missing")
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() > 64 {
		t.Fatalf("log exceeds the cap: %d", st.Size())
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("rotated log perms = %v, want 0600", st.Mode().Perm())
	}
}

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
