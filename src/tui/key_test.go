// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// TestKeyPressOfCtrl pins the loop's raw mapping for ctrl+letter: tcell's
// legacy keyboard reporting delivers it as a folded KeyCtrlX code (no
// rune, the modifier implicit), and the mapping must unbundle it to the
// canonical binding name ("ctrl+f"). Advanced reporting (KeyRune +
// ModCtrl) must map identically - the binding lookup never sees the
// terminal's mode.
func TestKeyPressOfCtrl(t *testing.T) {
	legacy, _, ok := keyPressOf(tcell.NewEventKey(tcell.KeyCtrlF, "", tcell.ModCtrl))
	if !ok || legacy.String() != "ctrl+f" {
		t.Fatalf("legacy ctrl+f must map to the binding name, got %v (ok %v)", legacy, ok)
	}
	advanced, _, ok := keyPressOf(tcell.NewEventKeyEx(tcell.KeyRune, "f", tcell.ModCtrl, true, 0, 1))
	if !ok || advanced.String() != "ctrl+f" {
		t.Fatalf("advanced ctrl+f must map identically, got %v (ok %v)", advanced, ok)
	}
	plain, _, ok := keyPressOf(tcell.NewEventKey(tcell.KeyRune, "j", tcell.ModNone))
	if !ok || plain.String() != "j" {
		t.Fatalf("plain j must keep its text, got %v (ok %v)", plain, ok)
	}
}
