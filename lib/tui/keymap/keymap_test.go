// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package keymap

import (
	"reflect"
	"testing"
)

func TestBindingAcceptsNotmuttShapesAndRejectsUnknownFields(t *testing.T) {
	for _, tc := range []struct {
		input any
		want  Binding
	}{
		{"quit", Binding{Fun: "quit"}},
		{[]any{"quit", "Leave the client"}, Binding{Fun: "quit", Desc: "Leave the client"}},
		{map[string]any{"fun": "quit", "desc": "Leave the client", "show": true, "inherit": true}, Binding{Fun: "quit", Desc: "Leave the client", Show: true, Inherit: true}},
	} {
		var got Binding
		if err := got.UnmarshalTOML(tc.input); err != nil || got != tc.want {
			t.Fatalf("binding %#v = %#v, %v", tc.input, got, err)
		}
	}
	for _, bad := range []any{[]any{"quit"}, map[string]any{"fun": "quit", "unknown": true}, map[string]any{"fun": 12}, true} {
		var got Binding
		if err := got.UnmarshalTOML(bad); err == nil {
			t.Fatalf("accepted malformed binding %#v", bad)
		}
	}
}

func TestCompilePreservesCaseAndOptInInheritance(t *testing.T) {
	scheme := map[string]map[string]Binding{
		"index": {"j": {Fun: "down", Desc: "Move down"}, "J": {Fun: "next", Desc: "Next entry", Show: true, Inherit: true}, "q": {Fun: "quit", Desc: "Quit", Show: true}},
		"pager": {"j": {Fun: "scroll", Desc: "Scroll", Show: true}, "x": {Fun: "close", Desc: "Close", Show: true}},
	}
	compiled, err := Compile(scheme, map[string]string{"pager": "index"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := compiled.Action("index", "j"); got != "down" {
		t.Fatalf("lowercase dispatch = %q", got)
	}
	if got, _ := compiled.Action("index", "J"); got != "next" {
		t.Fatalf("uppercase dispatch = %q", got)
	}
	if got, _ := compiled.Action("pager", "J"); got != "next" {
		t.Fatalf("inherited action = %q", got)
	}
	if got, _ := compiled.Action("pager", "j"); got != "scroll" {
		t.Fatalf("child override = %q", got)
	}
	if _, ok := compiled.Action("pager", "q"); ok {
		t.Fatal("non-inheritable quit leaked into pager")
	}
	if got := compiled.KeyFor("pager", "next"); got != "J" {
		t.Fatalf("reverse binding = %q", got)
	}
	if got := compiled.Hints("pager"); !reflect.DeepEqual(got, []Entry{{Key: "j", Fun: "scroll", Desc: "Scroll", Show: true}, {Key: "x", Fun: "close", Desc: "Close", Show: true}}) {
		t.Fatalf("pager hints = %#v", got)
	}
	if got := compiled.Entries("pager"); len(got) != 3 || got[0].Key != "J" || got[0].Desc != "Next entry" || got[0].Show {
		t.Fatalf("full contextual help = %#v", got)
	}
}

func TestCompileRejectsInvalidKeysAndUnknownParents(t *testing.T) {
	for _, tc := range []struct {
		scheme  map[string]map[string]Binding
		parents map[string]string
	}{
		{map[string]map[string]Binding{"index": {"": {Fun: "quit"}}}, nil},
		{map[string]map[string]Binding{"index": {"q": {Fun: " "}}}, nil},
		{map[string]map[string]Binding{"pager": {"j": {Fun: "scroll"}}}, map[string]string{"pager": "missing"}},
		{map[string]map[string]Binding{"a": {"x": {Fun: "go"}}, "b": {"y": {Fun: "go"}}}, map[string]string{"a": "b", "b": "a"}},
	} {
		if _, err := Compile(tc.scheme, tc.parents); err == nil {
			t.Fatalf("accepted invalid keymap: %#v", tc)
		}
	}
}

func TestResolvePrefersTypedRuneThenNamedKey(t *testing.T) {
	bindings := map[string]string{"J": "next", "j": "down", "ctrl+n": "down"}
	if got := Resolve(bindings, "J", "j"); got != "next" {
		t.Fatalf("uppercase action = %q", got)
	}
	if got := Resolve(bindings, "", "ctrl+n"); got != "down" {
		t.Fatalf("named key action = %q", got)
	}
}
