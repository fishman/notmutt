module notmutt

go 1.26.5

require (
	github.com/BurntSushi/toml v1.4.0
	github.com/charmbracelet/lipgloss v0.13.0
	github.com/emersion/go-message v0.18.2
	github.com/fishman/go.notmuch v0.0.0-20260815164219-528176721928
	github.com/gdamore/tcell/v2 v2.13.10
	github.com/gen2brain/beeep v0.11.2
	github.com/godbus/dbus/v5 v5.1.0
	github.com/mattn/go-runewidth v0.0.23
	github.com/muesli/termenv v0.15.2
	github.com/sahilm/fuzzy v0.1.3
	github.com/yuin/gopher-lua v1.1.2
	go.etcd.io/bbolt v1.3.11
)

// vendored build input: the fishman fork (notmuch/bindings/go.notmuch in
// this workspace), pinned by the workspace checkout's git history; never
// fetched from the proxy.
replace github.com/fishman/go.notmuch => ../notmuch/bindings/go.notmuch

require (
	git.sr.ht/~jackmordaunt/go-toast v1.1.2 // indirect
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/charmbracelet/x/ansi v0.11.7 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/esiqveland/notify v0.13.3 // indirect
	github.com/gdamore/encoding v1.0.1 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/jackmordaunt/icns/v3 v3.0.1 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/nfnt/resize v0.0.0-20180221191011-83c6a9932646 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/sergeymakinen/go-bmp v1.0.0 // indirect
	github.com/sergeymakinen/go-ico v1.0.0-beta.0 // indirect
	github.com/tadvi/systray v0.0.0-20190226123456-11a2b8fa57af // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/term v0.37.0 // indirect
	golang.org/x/text v0.31.0 // indirect
)
