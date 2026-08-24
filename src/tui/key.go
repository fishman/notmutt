// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gdamore/tcell/v3"
)

// The key model is the tea v2 shape with the tea import gone (record
// 23): Text the printable string (empty for special and ctrl/alt
// keys), Code the rune or a special key code, Mod the modifiers;
// String() is the canonical binding name ("j", "ctrl+d", "pgup") -
// actionForKey's second probe. The loop maps tcell events into these;
// tests construct presses directly.

type keyMod int

const (
	modCtrl keyMod = 1 << iota
	modAlt
	modShift
	modNone keyMod = 0
)

// Special key codes (untyped constants, the pressType test helper
// passes them as runes). The values live above the printable range;
// only String() and the loop's mapping care about them.
const (
	KeyUp = rune(0xE001) + iota
	KeyDown
	KeyLeft
	KeyRight
	KeyPgUp
	KeyPgDown
	KeyHome
	KeyEnd
	KeyInsert
	KeyDelete
	KeyEnter
	KeyTab
	KeyEsc
	KeyBackspace
	KeySpace
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
)

// KeyPressMsg is one key press; String() mirrors ultraviolet's canonical naming so the binding tables keep their vocabulary.
type KeyPressMsg struct {
	Text string // the printable text, empty for special and ctrl/alt keys
	Code rune   // the rune or a special key code
	Mod  keyMod
}

func (k KeyPressMsg) String() string {
	if k.Text != "" && k.Text != " " {
		return k.Text
	}
	var b strings.Builder
	if k.Mod&modCtrl != 0 {
		b.WriteString("ctrl+")
	}
	if k.Mod&modAlt != 0 {
		b.WriteString("alt+")
	}
	if k.Mod&modShift != 0 {
		b.WriteString("shift+")
	}
	if name, ok := specialKeyName[k.Code]; ok {
		b.WriteString(name)
	} else {
		b.WriteRune(k.Code)
	}
	return b.String()
}

// KeyReleaseMsg is a key release (kitty protocol release reporting).
// tcell does not deliver release events (record 23); the type survives
// so the model's release path stays wired, and the legendTick fallback
// settles terminals without it.
type KeyReleaseMsg KeyPressMsg

var specialKeyName = map[rune]string{
	KeyEnter: "enter", KeyTab: "tab", KeyBackspace: "backspace",
	KeyEsc: "esc", KeySpace: "space", KeyUp: "up", KeyDown: "down",
	KeyLeft: "left", KeyRight: "right", KeyPgUp: "pgup", KeyPgDown: "pgdown",
	KeyHome: "home", KeyEnd: "end", KeyInsert: "insert", KeyDelete: "delete",
	KeyF1: "f1", KeyF2: "f2", KeyF3: "f3", KeyF4: "f4", KeyF5: "f5", KeyF6: "f6",
	KeyF7: "f7", KeyF8: "f8", KeyF9: "f9", KeyF10: "f10", KeyF11: "f11", KeyF12: "f12",
}

// keyPressOf maps a tcell key event to a press (or release) message.
// Runes map with their text; special keys map to the special codes;
// ctrl/alt-modified keys carry no Text, so actionForKey's canonical
// probe resolves them.
//
// The shifted rune's case depends on the input path: legacy terminals
// deliver the shifted character itself ('H'), while the kitty protocol
// (OptAdvancedKeys, negotiated outside tmux) delivers the unshifted
// key with an explicit shift modifier - 'h' + ModShift. The shift is
// applied here so both paths bind "H" and "j" separately.
//
// The screen opens with OptAdvancedKeys (loop.go): tcell reports
// ctrl+letter as KeyRune with ModCtrl on every input path, so the
// legacy KeyCtrlA..KeyCtrlZ folding never reaches this mapping.
func keyPressOf(ev *tcell.EventKey) (KeyPressMsg, KeyReleaseMsg, bool) {
	code := ev.Key()
	mod := modNone
	if ev.Modifiers()&tcell.ModCtrl != 0 {
		mod |= modCtrl
	}
	if ev.Modifiers()&tcell.ModAlt != 0 {
		mod |= modAlt
	}
	if ev.Modifiers()&tcell.ModShift != 0 {
		mod |= modShift
	}
	if code == tcell.KeyRune {
		// v3 delivers the rune(s) as Str() (the Rune accessor is gone)
		r, _ := utf8.DecodeRuneInString(ev.Str())
		// kitty reports the unshifted key with an explicit shift
		// modifier; the text is the shifted character
		if mod&modShift != 0 {
			r = unicode.ToUpper(r)
		}
		text := ""
		if mod == modNone || mod == modShift {
			text = string(r)
		}
		if r == ' ' {
			return KeyPressMsg{Text: " ", Code: KeySpace, Mod: mod}, KeyReleaseMsg{}, true
		}
		return KeyPressMsg{Text: text, Code: r, Mod: mod}, KeyReleaseMsg{}, true
	}
	if k, ok := specialKeyCode[code]; ok {
		return KeyPressMsg{Code: k, Mod: mod}, KeyReleaseMsg{}, true
	}
	return KeyPressMsg{}, KeyReleaseMsg{}, false
}

var specialKeyCode = map[tcell.Key]rune{
	tcell.KeyUp: KeyUp, tcell.KeyDown: KeyDown,
	tcell.KeyLeft: KeyLeft, tcell.KeyRight: KeyRight,
	tcell.KeyPgUp: KeyPgUp, tcell.KeyPgDn: KeyPgDown,
	tcell.KeyHome: KeyHome, tcell.KeyEnd: KeyEnd,
	tcell.KeyInsert: KeyInsert, tcell.KeyDelete: KeyDelete,
	tcell.KeyEnter: KeyEnter, // KeyCR is the same value
	tcell.KeyTab:   KeyTab, tcell.KeyBacktab: KeyTab,
	tcell.KeyEsc:       KeyEsc, // KeyEscape is the same value
	tcell.KeyBackspace: KeyBackspace, tcell.KeyDEL: KeyBackspace,
	tcell.KeyF1: KeyF1, tcell.KeyF2: KeyF2, tcell.KeyF3: KeyF3,
	tcell.KeyF4: KeyF4, tcell.KeyF5: KeyF5, tcell.KeyF6: KeyF6,
	tcell.KeyF7: KeyF7, tcell.KeyF8: KeyF8, tcell.KeyF9: KeyF9,
	tcell.KeyF10: KeyF10, tcell.KeyF11: KeyF11, tcell.KeyF12: KeyF12,
}
