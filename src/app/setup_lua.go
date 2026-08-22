// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

// The setup template layer (R8, the R12 build-gating pattern): every
// detection template is a Lua file returning a table with name, match
// (the TOP-LEVEL folders that gate the template), and folders (tag ->
// candidate-folder paths for extraction). The shipped templates
// (lua/templates/) are embedded and evaluated as the built-ins;
// contributed templates from <configdir>/lua/templates are OPT-IN -
// only the names listed in [setup] templates load, replacing built-ins
// by name (the R2 preset override rule) or adding to the detection
// set. Compiles only under the lua build tag - default builds carry
// setup_lua_stub.go and the Go fallback in setup.Templates.
//
// The template shape (copy a file from lua/templates/ to
// <configdir>/lua/templates/, enable its name in [setup] templates,
// and edit):
//
//	return {
//	  name = "exchange",
//	  match = { "INBOX", "Sent Items" },
//	  folders = {
//	    inbox = { "INBOX" },
//	    sent = { "Sent Items" },
//	    deleted = { "Deleted Items" },
//	    draft = { "Drafts" },
//	    archive = { "Archive" },
//	    spam = { "Junk Email", "Junk" },
//	  },
//	}

package app

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"

	"notmutt/setup"
)

// builtinTemplates evaluates the shipped template files in the Go
// fallback's order: setup.Templates is the canonical sequence (the
// first match wins in Detect, so order is behavior), and the sync test
// pins the content equal. A leftover template not in the Go data would
// fail the pin test - appended sorted as a guard.
func builtinTemplates() []setup.Template {
	entries, err := fs.ReadDir(templateFS, "lua/templates")
	if err != nil {
		log.Printf("setup: embedded templates: %v", err)
		return nil
	}
	byName := map[string]setup.Template{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		src, err := fs.ReadFile(templateFS, "lua/templates/"+e.Name())
		if err != nil {
			log.Printf("setup template %s: %v", e.Name(), err)
			continue
		}
		if t, err := templateFromSource(src); err != nil {
			log.Printf("setup template %s: %v", e.Name(), err)
		} else {
			byName[t.Name] = t
		}
	}
	var out []setup.Template
	for _, want := range setup.Templates {
		if t, ok := byName[want.Name]; ok {
			out = append(out, t)
			delete(byName, want.Name)
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, byName[name])
	}
	return out
}

// luaTemplates loads the OPT-IN contributed templates from
// dir/templates: a file is one detection template, named <name>.lua,
// and only the names in active load - the seeded examples stay inert
// until [setup] templates names them. Sorted, so the detection order
// is deterministic. A listed file that fails to load is logged and
// skipped (a bad template never breaks setup). A missing dir is a
// no-op.
func luaTemplates(dir string, active []string) []setup.Template {
	tdir := filepath.Join(dir, "templates")
	entries, err := os.ReadDir(tdir)
	if err != nil {
		return nil
	}
	enabled := map[string]bool{}
	for _, name := range active {
		enabled[name] = true
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".lua" && enabled[strings.TrimSuffix(e.Name(), ".lua")] {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	var out []setup.Template
	for _, name := range files {
		src, err := os.ReadFile(filepath.Join(tdir, name))
		if err != nil {
			log.Printf("setup template %s: %v", name, err)
			continue
		}
		t, err := templateFromSource(src)
		if err != nil {
			log.Printf("setup template %s: %v", name, err)
			continue
		}
		if t.Name != strings.TrimSuffix(name, ".lua") {
			log.Printf("setup template %s: name %q must match the file name", name, t.Name)
			continue
		}
		out = append(out, t)
	}
	return out
}

// templateFromSource evaluates one template file's source. The VM
// opens no libs: a template is data (a returned table) - the sandbox
// has no surface at all. A fixed deadline aborts a busy-looping
// template (decision-record 20 kill switch); the load then fails and
// the template is skipped.
func templateFromSource(src []byte) (setup.Template, error) {
	vm := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer vm.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	vm.SetContext(ctx)
	if err := vm.DoString(string(src)); err != nil {
		return setup.Template{}, err
	}
	tbl, ok := vm.Get(-1).(*lua.LTable)
	if !ok {
		return setup.Template{}, fmt.Errorf("must return a table, got %s", vm.Get(-1).Type().String())
	}
	name, ok := tbl.RawGetString("name").(lua.LString)
	if !ok || name == "" {
		return setup.Template{}, fmt.Errorf("name must be a non-empty string")
	}
	t := setup.Template{Name: string(name)}
	t.Match = stringList(tbl.RawGetString("match"))
	t.Folders = tagFolders(tbl.RawGetString("folders"))
	t.NoFcc = lua.LVAsBool(tbl.RawGetString("no_fcc"))
	if len(t.Match) == 0 {
		return setup.Template{}, fmt.Errorf("match must name the top-level folders that gate the template")
	}
	if len(t.Folders) == 0 {
		return setup.Template{}, fmt.Errorf("folders must map tags to candidate folder names")
	}
	return t, nil
}

// stringList converts a Lua table of strings (the match names).
func stringList(v lua.LValue) []string {
	tbl, ok := v.(*lua.LTable)
	if !ok {
		return nil
	}
	var out []string
	tbl.ForEach(func(_, v lua.LValue) {
		out = append(out, v.String())
	})
	return out
}

// tagFolders converts a Lua table of tag -> candidate-name arrays.
// Non-table values are skipped - the loader's checks above enforce the
// strict shape.
func tagFolders(v lua.LValue) map[string][]string {
	tbl, ok := v.(*lua.LTable)
	if !ok {
		return nil
	}
	out := map[string][]string{}
	tbl.ForEach(func(k, v lua.LValue) {
		list, ok := v.(*lua.LTable)
		if !ok {
			return
		}
		var cands []string
		list.ForEach(func(_, c lua.LValue) {
			cands = append(cands, c.String())
		})
		out[k.String()] = cands
	})
	return out
}
