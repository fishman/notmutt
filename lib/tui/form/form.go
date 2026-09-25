// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package form

import (
	"errors"
	"maps"
	"slices"
	"unicode"
	"unicode/utf8"

	"github.com/fishman/notmutt/lib/tui/table"
	"github.com/mattn/go-runewidth"
)

type Kind uint8

const (
	Text Kind = iota
	Toggle
	Choice
)

type Field struct {
	ID, Label, Value string
	Kind             Kind
	Choices          []string
	Sensitive        bool
	ReadOnly         bool
}

type Change struct{ ID, Value string }

type Row struct {
	ID, Text string
	Selected bool
}

type Form struct {
	fields           []Field
	selected, offset int
	cursor           int
	editing          bool
	edited           map[string]string
}

func New(fields []Field) (*Form, error) {
	if len(fields) == 0 || len(fields) > 64 {
		return nil, errors.New("form: expected 1-64 fields")
	}
	f := &Form{fields: make([]Field, len(fields)), edited: make(map[string]string)}
	seen := make(map[string]bool, len(fields))
	for i, field := range fields {
		if field.ID == "" || field.Label == "" || seen[field.ID] || len(field.Value) > 4096 {
			return nil, errors.New("form: invalid field")
		}
		seen[field.ID] = true
		switch field.Kind {
		case Text:
		case Toggle:
			if field.Value != "true" && field.Value != "false" {
				return nil, errors.New("form: invalid toggle")
			}
		case Choice:
			if len(field.Choices) == 0 || !slices.Contains(field.Choices, field.Value) {
				return nil, errors.New("form: invalid choice")
			}
		default:
			return nil, errors.New("form: unknown field kind")
		}
		field.Choices = slices.Clone(field.Choices)
		f.fields[i] = field
	}
	return f, nil
}

func (f *Form) Clone() *Form {
	if f == nil {
		return nil
	}
	copy := *f
	copy.fields = slices.Clone(f.fields)
	for i := range copy.fields {
		copy.fields[i].Choices = slices.Clone(f.fields[i].Choices)
	}
	copy.edited = maps.Clone(f.edited)
	return &copy
}

func (f *Form) Move(delta int) {
	if f == nil || len(f.fields) == 0 {
		return
	}
	f.selected = min(max(f.selected+delta, 0), len(f.fields)-1)
	f.cursor = len(f.value())
	f.editing = false
}

func (f *Form) value() string {
	field := f.fields[f.selected]
	if value, ok := f.edited[field.ID]; ok {
		return value
	}
	return field.Value
}

func (f *Form) set(value string) {
	field := f.fields[f.selected]
	if value == field.Value {
		delete(f.edited, field.ID)
	} else {
		f.edited[field.ID] = value
	}
}

func (f *Form) Toggle() bool {
	if f == nil || f.fields[f.selected].ReadOnly || f.fields[f.selected].Kind != Toggle {
		return false
	}
	if f.value() == "true" {
		f.set("false")
	} else {
		f.set("true")
	}
	return true
}

func (f *Form) Cycle(delta int) bool {
	if f == nil || f.fields[f.selected].ReadOnly || f.fields[f.selected].Kind != Choice {
		return false
	}
	choices := f.fields[f.selected].Choices
	index := slices.Index(choices, f.value())
	if index < 0 {
		return false
	}
	index = ((index+delta)%len(choices) + len(choices)) % len(choices)
	f.set(choices[index])
	return true
}

func (f *Form) SetText(value string) bool {
	if f == nil || f.fields[f.selected].ReadOnly || f.fields[f.selected].Kind != Text || len(value) > 4096 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	f.set(value)
	f.cursor = len(value)
	f.editing = true
	return true
}

func (f *Form) Insert(text string) bool {
	if f == nil || !utf8.ValidString(text) || f.fields[f.selected].ReadOnly || f.fields[f.selected].Kind != Text {
		return false
	}
	value, cursor := f.value(), f.cursor
	if !f.editing {
		cursor = len(value)
	}
	if !f.SetText(value[:cursor] + text + value[cursor:]) {
		return false
	}
	f.cursor = cursor + len(text)
	return true
}

func (f *Form) Backspace() bool {
	if f == nil || f.fields[f.selected].ReadOnly || f.fields[f.selected].Kind != Text {
		return false
	}
	value, cursor := f.value(), f.cursor
	if !f.editing {
		cursor = len(value)
	}
	if cursor <= 0 {
		return false
	}
	_, size := utf8.DecodeLastRuneInString(value[:cursor])
	if !f.SetText(value[:cursor-size] + value[cursor:]) {
		return false
	}
	f.cursor = cursor - size
	return true
}

func (f *Form) MoveCursor(delta int) {
	if f == nil || f.fields[f.selected].Kind != Text || f.fields[f.selected].ReadOnly {
		return
	}
	value := f.value()
	if !f.editing {
		f.cursor = len(value)
		f.editing = true
	}
	for ; delta > 0 && f.cursor < len(value); delta-- {
		_, size := utf8.DecodeRuneInString(value[f.cursor:])
		f.cursor += size
	}
	for ; delta < 0 && f.cursor > 0; delta++ {
		_, size := utf8.DecodeLastRuneInString(value[:f.cursor])
		f.cursor -= size
	}
}

func (f *Form) Changes() []Change {
	if f == nil || len(f.edited) == 0 {
		return nil
	}
	changes := make([]Change, 0, len(f.edited))
	for _, field := range f.fields {
		if value, ok := f.edited[field.ID]; ok {
			changes = append(changes, Change{ID: field.ID, Value: value})
		}
	}
	return changes
}

func (f *Form) Cancel() {
	if f != nil {
		clear(f.edited)
		f.editing = false
		f.cursor = 0
	}
}

func (f *Form) Rows(width, height int) []Row {
	if f == nil || width < 3 || height < 1 {
		return nil
	}
	if f.selected < f.offset {
		f.offset = f.selected
	}
	if f.selected >= f.offset+height {
		f.offset = f.selected - height + 1
	}
	labelWidth := min(24, max(6, width/3))
	labelWidth = min(labelWidth, width-2)
	layout := table.Layout{Cols: []table.Col{{Floor: labelWidth, Cap: labelWidth}}, Sep: "  "}
	sizes := layout.Sizes(width, true)
	end := min(len(f.fields), f.offset+height)
	rows := make([]Row, 0, end-f.offset)
	for index := f.offset; index < end; index++ {
		field := f.fields[index]
		value, changed := f.edited[field.ID]
		if !changed {
			value = field.Value
		}
		if field.Sensitive {
			if changed && value != "" {
				value = "[hidden]"
			} else {
				value = "(unchanged)"
			}
		}
		text := layout.Line([]string{field.Label, value}, sizes)
		rows = append(rows, Row{ID: field.ID, Text: runewidth.Truncate(text, width, ""), Selected: index == f.selected})
	}
	return rows
}
