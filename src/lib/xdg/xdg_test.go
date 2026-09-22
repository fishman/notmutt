package xdg

import "testing"

func TestRuntimeHomeUsesEnvironment(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/alpha")
	if got := RuntimeHome(); got != "/run/user/alpha" {
		t.Fatalf("RuntimeHome() = %q", got)
	}
}

func TestStateAndDataHomesUseEnvironment(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/state/alpha")
	t.Setenv("XDG_DATA_HOME", "/data/alpha")

	if got := StateHome(); got != "/state/alpha" {
		t.Fatalf("StateHome() = %q", got)
	}
	if got := DataHome(); got != "/data/alpha" {
		t.Fatalf("DataHome() = %q", got)
	}
}
