package form

import (
	"strings"
	"testing"
)

func TestFormTogglesAndMasksSensitiveValues(t *testing.T) {
	f, err := New([]Field{
		{ID: "url", Label: "Source URL", Kind: Text, Sensitive: true},
		{ID: "enabled", Label: "Enabled", Kind: Toggle, Value: "false"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !f.SetText("https://private.invalid/?token=secret") {
		t.Fatal("sensitive text edit rejected")
	}
	f.Move(1)
	if !f.Toggle() || len(f.Changes()) != 2 {
		t.Fatal("toggle or text edit was lost")
	}
	for _, row := range f.Rows(40, 2) {
		if strings.Contains(row.Text, "secret") || strings.Contains(row.Text, "private.invalid") {
			t.Fatal("sensitive value rendered")
		}
	}
	f.Cancel()
	if len(f.Changes()) != 0 {
		t.Fatal("cancel retained pending edits")
	}
}

func TestFormChoicesAndSelectionSurviveScrollingAndClone(t *testing.T) {
	fields := []Field{
		{ID: "id", Label: "ID", Kind: Text, Value: "stable", ReadOnly: true},
		{ID: "name", Label: "Name", Kind: Text},
		{ID: "source", Label: "Source", Kind: Text, Sensitive: true},
		{ID: "enabled", Label: "Enabled", Kind: Toggle, Value: "true"},
		{ID: "route", Label: "Route", Kind: Choice, Value: "direct", Choices: []string{"direct", "system_proxy"}},
	}
	f, err := New(fields)
	if err != nil {
		t.Fatal(err)
	}
	f.Move(4)
	rows := f.Rows(36, 2)
	if len(rows) != 2 || rows[1].ID != "route" || !rows[1].Selected {
		t.Fatalf("selected field invisible after scrolling: %+v", rows)
	}
	clone := f.Clone()
	if !clone.Cycle(1) || len(clone.Changes()) != 1 || clone.Changes()[0] != (Change{ID: "route", Value: "system_proxy"}) {
		t.Fatal("choice change did not produce one stable field edit")
	}
	if len(f.Changes()) != 0 || fields[4].Value != "direct" {
		t.Fatal("clone changed original form or field input")
	}
}

func TestFormRejectsDuplicateFieldsAndInvalidChoices(t *testing.T) {
	for _, fields := range [][]Field{
		{{ID: "one", Kind: Text}, {ID: "one", Kind: Toggle, Value: "true"}},
		{{ID: "mode", Kind: Choice, Value: "unknown", Choices: []string{"direct"}}},
	} {
		if _, err := New(fields); err == nil {
			t.Fatal("invalid form accepted")
		}
	}
}

func TestFormBackspaceAndToggleUndoPreserveText(t *testing.T) {
	f, err := New([]Field{{ID: "name", Label: "Name", Kind: Text}, {ID: "enabled", Label: "Enabled", Kind: Toggle, Value: "true"}})
	if err != nil {
		t.Fatal(err)
	}
	if !f.Insert("\u4e2d\u56fd") || !f.Backspace() || f.Changes()[0] != (Change{ID: "name", Value: "\u4e2d"}) {
		t.Fatal("backspace split a wide rune")
	}
	f.Move(1)
	if !f.Toggle() || !f.Toggle() || len(f.Changes()) != 1 {
		t.Fatal("toggle reversal left a redundant edit")
	}
}
