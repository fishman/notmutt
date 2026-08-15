//go:build !cli

package notmuch

// New is the runtime backend factory: cgo by default, the CLI behind
// -tags cli as the F10 escape hatch (decision record 3).
func New() Backend { return NewCGO() }
