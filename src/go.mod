module notmutt

go 1.26.5

require (
	github.com/BurntSushi/toml v1.4.0
	github.com/charmbracelet/bubbletea v1.1.0
	github.com/emersion/go-message v0.18.2
	github.com/mattn/go-runewidth v0.0.16
	github.com/zenhack/go.notmuch v0.0.0-20260814090000-000000000000
	go.etcd.io/bbolt v1.3.11
)

// vendored build input: the fishman fork (notmuch/bindings/go.notmuch in
// this workspace), pinned by the workspace checkout's git history; never
// fetched from the proxy. The upstream module path is zenhack's.
replace github.com/zenhack/go.notmuch => ../notmuch/bindings/go.notmuch

require (
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/charmbracelet/lipgloss v0.13.0 // indirect
	github.com/charmbracelet/x/ansi v0.2.3 // indirect
	github.com/charmbracelet/x/term v0.2.0 // indirect
	github.com/erikgeiser/coninput v0.0.0-20211004153227-1c3628e74d0f // indirect
	github.com/lucasb-eyer/go-colorful v1.2.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-localereader v0.0.1 // indirect
	github.com/muesli/ansi v0.0.0-20230316100256-276c6243b2f6 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/muesli/termenv v0.15.2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	golang.org/x/sync v0.8.0 // indirect
	golang.org/x/sys v0.24.0 // indirect
	golang.org/x/text v0.14.0 // indirect
)
