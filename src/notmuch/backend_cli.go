//go:build cli

package notmuch

// New is the runtime backend factory under the cli build tag.
func New() Backend { return NewCLI() }
