module kilo-local

go 1.26.0

require (
	github.com/pelletier/go-toml/v2 v2.4.3
	github.com/tailscale/hujson v0.0.0-20260727124030-b80ff77dac4f
	github.com/zalando/go-keyring v0.2.6
	golang.org/x/image v0.26.0
)

require (
	gioui.org/shader v1.0.9 // indirect
	github.com/go-text/typesetting v0.3.4 // indirect
	golang.org/x/exp/shiny v0.0.0-20250408133849-7e4ce0ab07d0 // indirect
	golang.org/x/net v0.48.0 // indirect
	golang.org/x/text v0.32.0 // indirect
)

require (
	al.essio.dev/pkg/shellescape v1.6.0 // indirect
	fyne.io/systray v1.12.3-0.20260810170012-af4e8e793ec4
	gioui.org v0.10.2
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace gioui.org => ./third_party/gio
