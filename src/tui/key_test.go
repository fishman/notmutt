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

// TestKeyPressOfShift pins the shift modifier on the text: the shift
// rides Mod, never folded into the letter. Legacy terminals deliver
// the shifted character itself ('H' + ModNone); the kitty protocol
// (negotiated outside tmux) delivers the base key with an explicit
// shift modifier ('h' + ModShift) - the case distinction is the
// binding's problem, not the mapper's.
func TestKeyPressOfShift(t *testing.T) {
	kitty, _, ok := keyPressOf(tcell.NewEventKeyEx(tcell.KeyRune, "h", tcell.ModShift, true, 0, 1))
	if !ok || kitty.Text != "h" || kitty.Mod&tcell.ModShift == 0 {
		t.Fatalf("kitty shift+h must keep its base text and the shift mod, got %v (ok %v)", kitty, ok)
	}
	legacy, _, ok := keyPressOf(tcell.NewEventKey(tcell.KeyRune, "H", tcell.ModNone))
	if !ok || legacy.Text != "H" || legacy.String() != "H" {
		t.Fatalf("legacy H must keep its text, got %v (ok %v)", legacy, ok)
	}
	// a shifted rune stays verbatim (no double fold anywhere)
	shifted, _, ok := keyPressOf(tcell.NewEventKeyEx(tcell.KeyRune, "H", tcell.ModShift, true, 0, 1))
	if !ok || shifted.Text != "H" {
		t.Fatalf("shift+uppercase must stay H, got %v (ok %v)", shifted, ok)
	}
	// non-letter with shift keeps its text
	dig, _, ok := keyPressOf(tcell.NewEventKeyEx(tcell.KeyRune, "1", tcell.ModShift, true, 0, 1))
	if !ok || dig.Text != "1" {
		t.Fatalf("shift+1 must stay 1, got %v (ok %v)", dig, ok)
	}
}
