package xdg

import "testing"

func TestHomesUseEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, env, value string
		resolve          func() string
	}{
		{"runtime", "XDG_RUNTIME_DIR", "/run/user/alpha", RuntimeHome},
		{"state", "XDG_STATE_HOME", "/state/alpha", StateHome},
		{"data", "XDG_DATA_HOME", "/data/alpha", DataHome},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.env, tc.value)
			if got := tc.resolve(); got != tc.value {
				t.Fatalf("%s = %q, want %q", tc.name, got, tc.value)
			}
		})
	}
}

func TestRuntimeOrStateFallsBackToState(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("XDG_STATE_HOME", "/state/alpha")
	if got := RuntimeOrState(); got != "/state/alpha" {
		t.Fatalf("RuntimeOrState() = %q", got)
	}
}
