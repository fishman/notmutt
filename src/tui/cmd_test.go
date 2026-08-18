// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestExecCmdWiresStdio pins the exec.Command contract this migration
// must not drop: a foreground TUI child (the editor, the attach picker)
// gets the parent's terminal on all three fds. exec.Command wires nil
// stdio to /dev/null - the child would launch invisible and unreadable
// (the tea ExecProcess pattern, cmd.go).
func TestExecCmdWiresStdio(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc/self/fd is linux-specific")
	}
	want := []string{fdTarget(t, 0), fdTarget(t, 1), fdTarget(t, 2)}
	report := filepath.Join(t.TempDir(), "fds")
	// the child reports its fd targets through fd 3; any output redirect
	// would clobber the measured fd, so fds 0-2 are aliased to 3/4/5
	// before the report opens (the alias is a dup of the original fd and
	// resolves to the same target); the path rides as $1, never
	// interpolated into the script
	c := exec.Command("sh", "-c",
		`exec 3<&0
exec 4<&1
exec 5<&2
exec 6> "$1"
readlink /proc/self/fd/3 >&6
readlink /proc/self/fd/4 >&6
readlink /proc/self/fd/5 >&6`, "sh", report)
	execCmd(c, func(err error) any { return err })()

	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("fd %d = %q, want the parent's %q (nil stdio is /dev/null)", i, got[i], want[i])
		}
	}
}

func fdTarget(t *testing.T, fd int) string {
	t.Helper()
	p, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", fd))
	if err != nil {
		t.Fatal(err)
	}
	return p
}
