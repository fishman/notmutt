package tui

// onApply is the apply seam: the app wires it with SetApplyHandler; the
// default is a no-op so the model works in tests.
var onApply = func() {}

func SetApplyHandler(fn func()) {
	onApply = fn
}
