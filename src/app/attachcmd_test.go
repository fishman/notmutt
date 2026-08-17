package app

import (
	"reflect"
	"testing"

	"notmutt/config"
	"notmutt/tui"
)

func resetAttachcmds() {
	attachcmdsMu.Lock()
	defer attachcmdsMu.Unlock()
	attachcmds = map[string][]string{}
	attachcmdsOrder = nil
}

func TestAttachCommandRegistry(t *testing.T) {
	resetAttachcmds()

	registerAttachCommand("yazi", []string{"yazi", "--chooser-file"})
	registerAttachCommand("", []string{"bad"})
	registerAttachCommand("nope", nil)
	registerAttachCommand("fzf", []string{"fzf"})

	want := []tui.AttachCommand{
		{Name: "yazi", Argv: []string{"yazi", "--chooser-file"}},
		{Name: "fzf", Argv: []string{"fzf"}},
	}
	if snap := attachCommandSnapshot(); !reflect.DeepEqual(snap, want) {
		t.Fatalf("snapshot = %+v, want %+v", snap, want)
	}

	// the snapshot must be a copy - mutating it cannot leak into the registry
	snap := attachCommandSnapshot()
	snap[0].Argv[0] = "hacked"
	snap[0].Argv = nil
	if got := attachCommandSnapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("mutating a snapshot must not leak into the registry: %+v", got)
	}
}

func TestLoadConfigAttachCommands(t *testing.T) {
	resetAttachcmds()

	cfg := config.Default()
	// the TOML table is unordered; registration order is the sorted
	// name order - deterministic
	cfg.AttachCommands = map[string][]string{"yazi": {"yazi"}, "ranger": {"ranger"}}
	loadConfigAttachCommands(cfg)

	want := []string{"ranger", "yazi"}
	snap := attachCommandSnapshot()
	if len(snap) != 2 || snap[0].Name != want[0] || snap[1].Name != want[1] {
		t.Fatalf("config commands must register in sorted order: %+v", snap)
	}
}
