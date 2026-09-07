# Kilo Proxy artwork

The geometric K was selected from concept A. Its thick rounded stem, central notch, separated diagonal arms and citrus upper-right accent are shared by the native app, website, tray and release bundles.

The canonical vector geometry and palette live in `internal/brand/brand.go`. The PNGs have real transparency; no concept-board background is included. Colors are forest `#202720`, citrus `#e8f36a` and ivory `#f4f5ef`.

Regenerate the checked-in assets from the repository root:

```sh
go run ./cmd/icon-assets
go test ./internal/brand
```

- `logo-mark.svg` and `logo-mark.png`: isolated transparent K.
- `icon.svg` and `icon.png`: K on the ivory application tile; native sidebar, Linux launcher, macOS bundle and Windows executable use this artwork.
- `tray.png`: small color tile for Windows/Linux notification areas.
- `tray-template.png`: monochrome alpha silhouette for macOS menu-bar adaptation.

The Windows packager embeds the application icon and GUI manifest using the pinned build tool `github.com/tc-hib/go-winres@v0.3.3`. The generated COFF resource is temporary and removed after compilation. It adds no runtime dependency or installer to the executable.
