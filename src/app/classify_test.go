// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"os"
	"testing"
)

func TestLastClassifyRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := writeLastClassify(7); err != nil {
		t.Fatal(err)
	}
	got, ok := readLastClassify()
	if !ok || got != 7 {
		t.Fatalf("read = %d, %v, want 7, true", got, ok)
	}
}

func TestLastClassifyMissing(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if _, ok := readLastClassify(); ok {
		t.Fatal("a missing file must read as no-L")
	}
}

func TestLastClassifyCorrupt(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := lastClassifyPath()
	if err := os.MkdirAll(dir[:len(dir)-len("last-classify")], 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not-a-number"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readLastClassify(); ok {
		t.Fatal("a corrupt file must read as no-L (re-classify the bracket, never a full backfill)")
	}
}

func TestLastClassifyNeverRegresses(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := writeLastClassify(10); err != nil {
		t.Fatal(err)
	}
	if err := writeLastClassify(5); err != nil {
		t.Fatal(err)
	}
	got, ok := readLastClassify()
	if !ok || got != 10 {
		t.Fatalf("floor regressed to %d, %v, want 10, true (a concurrent advance must win)", got, ok)
	}
}
