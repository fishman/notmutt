module notmutt

go 1.26.5

require (
	charm.land/bubbletea/v2 v2.0.8
	github.com/BurntSushi/toml v1.4.0
	github.com/charmbracelet/bubbletea v1.1.0
	github.com/charmbracelet/lipgloss v0.13.0
	github.com/emersion/go-message v0.18.2
	github.com/fishman/go.notmuch v0.0.0-20260814090000-000000000000
	github.com/mattn/go-runewidth v0.0.23
	github.com/muesli/termenv v0.15.2
	go.etcd.io/bbolt v1.3.11
)

// vendored build input: the fishman fork (notmuch/bindings/go.notmuch in
// this workspace), pinned by the workspace checkout's git history; never
// fetched from the proxy.
replace github.com/fishman/go.notmuch => ../notmuch/bindings/go.notmuch

require (
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20260703014108-f5a850f9c2b7 // indirect
	github.com/charmbracelet/x/ansi v0.11.7 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/erikgeiser/coninput v0.0.0-20211004153227-1c3628e74d0f // indirect
	github.com/lucasb-eyer/go-colorful v1.4.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-localereader v0.0.1 // indirect
	github.com/muesli/ansi v0.0.0-20230316100256-276c6243b2f6 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.14.0 // indirect
)
