package tui

// onTagOp is the worker seam: the app wires it with SetTagOpHandler; the
// default is a no-op so the model works in tests.
var onTagOp = func(msgID string, add bool) {}

func SetTagOpHandler(fn func(msgID string, add bool)) {
	onTagOp = fn
}

// onApply is the apply seam: the app wires it with SetApplyHandler; the
// default is a no-op so the model works in tests.
var onApply = func() {}

func SetApplyHandler(fn func()) {
	onApply = fn
}
