// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// TestKeyPressOfCtrl pins the loop's raw mapping for ctrl+letter: the
// screen opens with OptAdvancedKeys (loop.go), so tcell reports it as
// KeyRune with ModCtrl, mapping to the canonical binding name
// ("ctrl+f"). The legacy KeyCtrlX folding never reaches keyPressOf.
func TestKeyPressOfCtrl(t *testing.T) {
	advanced, _, ok := keyPressOf(tcell.NewEventKeyEx(tcell.KeyRune, "f", tcell.ModCtrl, true, 0, 1))
	if !ok || advanced.String() != "ctrl+f" {
		t.Fatalf("advanced ctrl+f must map identically, got %v (ok %v)", advanced, ok)
	}
	plain, _, ok := keyPressOf(tcell.NewEventKey(tcell.KeyRune, "j", tcell.ModNone))
	if !ok || plain.String() != "j" {
		t.Fatalf("plain j must keep its text, got %v (ok %v)", plain, ok)
	}
}
