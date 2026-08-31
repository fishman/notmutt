package aicmd

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadCommand(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "next.md",
		"---\n"+
			"name: Thread next steps\n"+
			"description: Summarize the next steps\n"+
			"provider: default\n"+
			"action: view\n"+
			"data: [participants, subjects, last_body]\n"+
			"account_context: true\n"+
			"---\n"+
			"Analyze this thread.\n")
	c, err := LoadCommand(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Thread next steps" || c.Description != "Summarize the next steps" || c.Provider != "default" || c.Action != "view" || !c.AccountContext {
		t.Fatalf("bad parse: %+v", c)
	}
	if len(c.Data) != 3 || c.Data[0] != "participants" || c.Data[2] != "last_body" {
		t.Fatalf("bad data: %v", c.Data)
	}
	if c.Body != "Analyze this thread." {
		t.Fatalf("bad body: %q", c.Body)
	}
}

func TestLoadCommandStrict(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"no frontmatter":   "name: x\n",
		"no closing":       "---\nname: x\n",
		"unknown key":      "---\nname: x\ndescription: y\naction: view\ndata: [count]\nbogus: 1\n---\nbody\n",
		"missing name":     "---\ndescription: y\naction: view\ndata: [count]\n---\nbody\n",
		"missing desc":     "---\nname: x\naction: view\ndata: [count]\n---\nbody\n",
		"missing action":   "---\nname: x\ndescription: y\ndata: [count]\n---\nbody\n",
		"bad action":       "---\nname: x\ndescription: y\naction: fire\ndata: [count]\n---\nbody\n",
		"bad data field":   "---\nname: x\ndescription: y\naction: view\ndata: [count, headers]\n---\nbody\n",
		"empty data":       "---\nname: x\ndescription: y\naction: view\ndata: []\n---\nbody\n",
		"empty body":       "---\nname: x\ndescription: y\naction: view\ndata: [count]\n---\n",
		"bad bool":         "---\nname: x\ndescription: y\naction: view\ndata: [count]\naccount_context: maybe\n---\nbody\n",
		"bad summary bool": "---\nname: x\ndescription: y\naction: view\ndata: [count]\nsummary_context: maybe\n---\nbody\n",
		"duplicate key":    "---\nname: x\nname: z\ndescription: y\naction: view\ndata: [count]\n---\nbody\n",
	}
	for label, content := range cases {
		path := write(t, dir, label+".md", content)
		if _, err := LoadCommand(path); err == nil {
			t.Errorf("%s: expected error", label)
		}
	}
	// the comment/blank case must actually parse cleanly
	path := write(t, dir, "ok.md",
		"---\n# a comment\n\nname: x\ndescription: y\naction: view\ndata: [count]\n---\nbody\n")
	if _, err := LoadCommand(path); err != nil {
		t.Errorf("comment case: %v", err)
	}
}

func TestLoadCommands(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "b.md", "---\nname: B\ndescription: b\naction: view\ndata: [count]\n---\nbody b\n")
	write(t, dir, "a.md", "---\nname: A\ndescription: a\naction: view\ndata: [count]\n---\nbody a\n")
	write(t, dir, "notes.txt", "not a prompt")
	cmds, err := LoadCommands(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 2 || cmds[0].Name != "A" || cmds[1].Name != "B" {
		t.Fatalf("bad order/set: %+v", cmds)
	}
	write(t, dir, "broken.md", "---\nname: X\n---\n")
	if _, err := LoadCommands(dir); err == nil {
		t.Fatal("expected broken-file error")
	}
}

func TestLoadCommandsDuplicateName(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.md", "---\nname: Same\ndescription: a\naction: view\ndata: [count]\n---\nbody a\n")
	write(t, dir, "b.md", "---\nname: Same\ndescription: b\naction: view\ndata: [count]\n---\nbody b\n")
	if _, err := LoadCommands(dir); err == nil {
		t.Fatal("expected duplicate-name error")
	}
}
