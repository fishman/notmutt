//go:build lua

// The setup template layer (R8, the R12 build-gating pattern): every
// detection template is a Lua file returning a table with name,
// required, and optional tag -> candidate-folder maps. The shipped
// templates (lua/templates/, the examples contributors copy) are
// embedded and evaluated as the built-ins; contributed templates from
// <configdir>/lua/templates replace built-ins by name (the R2 preset
// override rule), new names add to the detection set. Compiles only
// under the lua build tag - default builds carry setup_lua_stub.go,
// no Lua runtime, and the Go fallback in setup.Templates.
//
// The template shape (copy a file from lua/templates/ to
// <configdir>/lua/templates/ and edit):
//
//	return {
//	  name = "exchange",
//	  required = {
//	    inbox = { "INBOX" },
//	    sent = { "Sent Items" },
//	    deleted = { "Deleted Items" },
//	  },
//	  optional = {
//	    archive = { "Archive" },
//	    draft = { "Drafts" },
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
	"time"

	lua "github.com/yuin/gopher-lua"

	"notmutt/setup"
)

// builtinTemplates evaluates the shipped template files in the Go
// fallback's order: setup.Templates is the canonical sequence (the
// first match wins in Detect, so order is behavior), and the sync
// test pins the content equal. A leftover template not in the Go
// data would fail the pin test - appended sorted as a guard.
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

// luaTemplates loads every *.lua file in dir/templates (sorted, so
// the detection order is deterministic): each file is one detection
// template. A file that fails to load is logged and skipped (the
// plugin degrade rule - a bad template never breaks setup). A missing
// dir is a no-op - no contributed templates.
func luaTemplates(dir string) []setup.Template {
	tdir := filepath.Join(dir, "templates")
	entries, err := os.ReadDir(tdir)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".lua" {
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
		if t, err := templateFromSource(src); err != nil {
			log.Printf("setup template %s: %v", name, err)
		} else {
			out = append(out, t)
		}
	}
	return out
}

// templateFromSource evaluates one template file's source. The VM
// opens no libs: a template is data (a returned table) - there is
// nothing to call, so the sandbox has no surface at all. A fixed
// deadline aborts a busy-looping template (SetContext, the
// decision-record 20 kill switch); the load then fails and the
// template is skipped.
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
	t.Required = tagFolders(tbl.RawGetString("required"))
	t.Optional = tagFolders(tbl.RawGetString("optional"))
	if len(t.Required) == 0 {
		return setup.Template{}, fmt.Errorf("required must map tags to candidate folder names")
	}
	return t, nil
}

// tagFolders converts a Lua table of tag -> candidate-name arrays.
// Non-table values are skipped - the strict shape is the author's
// contract, enforced by the loader's checks above.
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
