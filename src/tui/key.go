// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v3"
)

// The key model mirrors tcell's EventKey (record 23): Text is the
// printable string (tcell Str(), shift folded into the letter and kept
// even for ctrl/alt presses), Code the tcell Key (KeyRune for
// printable keys, the special key otherwise), Mod the folded
// ctrl/alt/shift mask. String() is the canonical binding name ("j",
// "ctrl+d", "pgup") - actionForKey's second probe. The loop maps tcell
// events into these; tests construct presses directly. Text paths
// decide insertability via Typed(), never by Text emptiness.

type KeyPressMsg struct {
	Text string        // the printable string, raw Str() for rune keys
	Code tcell.Key     // KeyRune for printable keys, else the special key
	Mod  tcell.ModMask // folded ctrl/alt/shift
}

// Typed reports whether the press is a plain printable key (no
// ctrl/alt): the only presses a text field should accept and the only
// Text actionForKey probes.
func (k KeyPressMsg) Typed() bool {
	return k.Mod&(tcell.ModCtrl|tcell.ModAlt) == 0
}

func (k KeyPressMsg) String() string {
	if k.Typed() && k.Text != "" && k.Text != " " {
		return k.Text
	}
	var b strings.Builder
	if k.Mod&tcell.ModCtrl != 0 {
		b.WriteString("ctrl+")
	}
	if k.Mod&tcell.ModAlt != 0 {
		b.WriteString("alt+")
	}
	if k.Mod&tcell.ModShift != 0 {
		b.WriteString("shift+")
	}
	if name, ok := specialKeyName[k.Code]; ok {
		b.WriteString(name)
	} else {
		b.WriteString(k.Text)
	}
	return b.String()
}

// KeyReleaseMsg is a key release (kitty protocol release reporting).
// tcell does not deliver release events (record 23); the type survives
// so the model's release path stays wired, and the legendTick fallback
// settles terminals without it.
type KeyReleaseMsg KeyPressMsg

// specialKeyName is the canonical binding name for tcell's special
// keys. tcell's KeyNames uses display casing; these are the binding
// vocabularies ("pgup", "backspace", "f1") that actionForKey resolves.
var specialKeyName = map[tcell.Key]string{
	tcell.KeyEnter: "enter", tcell.KeyTab: "tab", tcell.KeyBackspace: "backspace",
	tcell.KeyEsc: "esc", tcell.KeySpace: "space", tcell.KeyUp: "up", tcell.KeyDown: "down",
	tcell.KeyLeft: "left", tcell.KeyRight: "right", tcell.KeyPgUp: "pgup", tcell.KeyPgDn: "pgdown",
	tcell.KeyHome: "home", tcell.KeyEnd: "end", tcell.KeyInsert: "insert", tcell.KeyDelete: "delete",
	tcell.KeyF1: "f1", tcell.KeyF2: "f2", tcell.KeyF3: "f3", tcell.KeyF4: "f4", tcell.KeyF5: "f5",
	tcell.KeyF6: "f6", tcell.KeyF7: "f7", tcell.KeyF8: "f8", tcell.KeyF9: "f9", tcell.KeyF10: "f10",
	tcell.KeyF11: "f11", tcell.KeyF12: "f12",
}

// keyPressOf maps a tcell key event to a press (or release) message.
// Rune keys carry their text verbatim (tcell Str()); the shift
// modifier rides Mod, never folded into the letter - legacy terminals
// deliver the shifted character itself ('H'), the kitty protocol the
// base key ('h' + ModShift). Special keys map to the tcell special
// key; the mod mask folds tcell's left/right variants onto the
// aggregate bits.
//
// The screen opens with OptAdvancedKeys (loop.go): tcell reports
// ctrl+letter as KeyRune with ModCtrl on every input path, so the
// legacy KeyCtrlA..KeyCtrlZ folding never reaches this mapping.
func keyPressOf(ev *tcell.EventKey) (KeyPressMsg, KeyReleaseMsg, bool) {
	mod := ev.Modifiers() & (tcell.ModCtrl | tcell.ModAlt | tcell.ModShift)
	code := ev.Key()
	if code == tcell.KeyRune {
		r, _ := utf8.DecodeRuneInString(ev.Str())
		if r == ' ' {
			return KeyPressMsg{Text: " ", Code: tcell.KeySpace, Mod: mod}, KeyReleaseMsg{}, true
		}
		return KeyPressMsg{Text: string(r), Code: tcell.KeyRune, Mod: mod}, KeyReleaseMsg{}, true
	}
	// fold legacy aliases onto the canonical special keys
	switch code {
	case tcell.KeyBacktab: // advanced mode folds it; the raw path keeps it
		code = tcell.KeyTab
	case tcell.KeyDEL: // KeyBackspace2 is the same value
		code = tcell.KeyBackspace
	}
	if _, ok := specialKeyName[code]; ok {
		return KeyPressMsg{Code: code, Mod: mod}, KeyReleaseMsg{}, true
	}
	return KeyPressMsg{}, KeyReleaseMsg{}, false
}
