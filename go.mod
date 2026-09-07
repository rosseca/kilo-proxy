module kilo-local

go 1.26.0

require (
	github.com/gogpu/systray v0.3.0
	github.com/pelletier/go-toml/v2 v2.4.3
	github.com/zalando/go-keyring v0.2.6
)

require (
	al.essio.dev/pkg/shellescape v1.5.1 // indirect
	github.com/danieljoos/wincred v1.2.2 // indirect
	github.com/go-webgpu/goffi v0.6.3 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/gogpu/systray => ./third_party/systray
