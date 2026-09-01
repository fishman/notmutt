// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WantMode asserts path exists with exactly the given permission bits.
func WantMode(t testing.TB, path string, perm os.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != perm {
		t.Errorf("%s mode = %v, want %04o", path, fi.Mode().Perm(), perm)
	}
}

// WantNot asserts none of nots appear in text.
func WantNot(t testing.TB, text string, nots ...string) {
	t.Helper()
	for _, not := range nots {
		if strings.Contains(text, not) {
			t.Errorf("unexpected %q in:\n%s", not, text)
		}
	}
}

// ReadDirNames lists a directory's entry names, failing the test on error.
func ReadDirNames(t testing.TB, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// WriteFile writes content to path, creating the parent dirs (0700) and the
// file (0600), failing the test on error. Returns path.
func WriteFile(t testing.TB, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
