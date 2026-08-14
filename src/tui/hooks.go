package tui

// onApply is the apply seam: the app wires it with SetApplyHandler; the
// default is a no-op so the model works in tests.
var onApply = func() {}

func SetApplyHandler(fn func()) {
	onApply = fn
}

// onOpen is the open seam: the app wires it with SetOpenHandler (the
// open key hands the thread id to the app, which loads the thread's
// messages and publishes ThreadLoaded); the default is a no-op so the
// model works in tests.
var onOpen = func(threadID string) {}

func SetOpenHandler(fn func(string)) {
	onOpen = fn
}
