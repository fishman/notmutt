package app

import (
	"reflect"
	"testing"

	"notmutt/config"
)

func TestAttachCommandRegistry(t *testing.T) {
	attachcmdsMu.Lock()
	attachcmds = map[string][]string{}
	attachcmdsMu.Unlock()

	registerAttachCommand("yazi", []string{"yazi", "--chooser-file"})
	registerAttachCommand("", []string{"bad"})
	registerAttachCommand("nope", nil)
	registerAttachCommand("fzf", []string{"fzf"})

	snap := attachCommandSnapshot()
	want := map[string][]string{
		"yazi": {"yazi", "--chooser-file"},
		"fzf":  {"fzf"},
	}
	if !reflect.DeepEqual(snap, want) {
		t.Fatalf("snapshot = %+v, want %+v", snap, want)
	}

	// the snapshot must be a copy - mutating it cannot leak into the registry
	snap["late"] = []string{"x"}
	snap = attachCommandSnapshot()
	if _, ok := snap["late"]; ok {
		t.Fatal("mutating a snapshot must not leak into the registry")
	}
}

func TestLoadConfigAttachCommands(t *testing.T) {
	attachcmdsMu.Lock()
	attachcmds = map[string][]string{}
	attachcmdsMu.Unlock()

	cfg := config.Default()
	cfg.AttachCommands = map[string][]string{"yazi": {"yazi"}}
	loadConfigAttachCommands(cfg)

	snap := attachCommandSnapshot()
	if _, ok := snap["yazi"]; !ok {
		t.Fatalf("config commands must register, got %+v", snap)
	}
}
