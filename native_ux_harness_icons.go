//go:build desktop

package main

import (
	"bytes"
	"image"
	"image/png"
	"path"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

// nativeAppIcon is one installed application's icon. Icons are read from the
// user's own apps at runtime, so no third-party artwork ships with Kilo Proxy.
type nativeAppIcon struct {
	loading bool
	ok      bool
	image   widget.Image
}

// uxhIconSources maps a harness to the launch clients whose bundle carries its
// icon. The CLIs borrow their desktop sibling's icon and add a terminal badge.
var uxhIconSources = map[string][]string{
	"codex": {"codex"}, "claude-desktop": {"claude-desktop"}, "synara": {"synara"}, "t3-code": {"t3-code"},
	"openmausbot": {"openmausbot"}, "zed": {"zed"}, "open-design": {"open-design"},
	"xcode":  {"xcode-chat", "xcode-codex", "xcode-claude"},
	"claude": {"claude-desktop"}, "codex-cli": {"codex"},
}

func uxhIconBadged(key string) bool { return key == "claude" || key == "codex-cli" }

// nativeAppBundle returns the .app bundle that contains path, or "".
func nativeAppBundle(name string) string {
	// Bundles are a macOS concept, so the path is always slash-separated.
	name = path.Clean(name)
	if !path.IsAbs(name) {
		return ""
	}
	bundle := ""
	for _, part := range strings.Split(name, "/") {
		bundle += part + "/"
		if strings.HasSuffix(part, ".app") {
			return path.Clean(bundle)
		}
	}
	return ""
}

// uxhIconBundle is the application bundle whose icon represents key.
func (u *nativeUI) uxhIconBundle(key string) string {
	clients := u.clientState().LaunchInfo.Clients
	for _, source := range uxhIconSources[key] {
		path := clients[source].Path
		if source == "codex" {
			if custom := strings.TrimSpace(u.value("clients-launch-app-path")); custom != "" {
				path = custom
			}
		}
		if bundle := nativeAppBundle(path); bundle != "" {
			return bundle
		}
	}
	return ""
}

// uxhAppIcon returns the loaded icon for key, starting a background load the
// first time a bundle is seen. A bundle that fails to load keeps the monogram.
func (u *nativeUI) uxhAppIcon(key string) (widget.Image, bool) {
	bundle := u.uxhIconBundle(key)
	if bundle == "" {
		return widget.Image{}, false
	}
	if u.appIcons == nil {
		u.appIcons = map[string]*nativeAppIcon{}
	}
	icon := u.appIcons[bundle]
	if icon == nil {
		icon = &nativeAppIcon{loading: true}
		u.appIcons[bundle] = icon
		go func() {
			var img image.Image
			data, err := nativeAppIconPNG(bundle)
			if err == nil {
				img, err = png.Decode(bytes.NewReader(data))
			}
			u.enqueue(func() {
				icon.loading = false
				if err == nil {
					icon.ok, icon.image = true, widget.Image{Src: paint.NewImageOp(img), Fit: widget.Contain}
				}
			})
		}()
	}
	return icon.image, icon.ok
}

// uxhAppIconLayout draws the application icon, with a small terminal badge in
// the corner for the command-line harnesses.
func (u *nativeUI) uxhAppIconLayout(key string, icon widget.Image, size unit.Dp) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		side := gtx.Dp(size)
		box := image.Pt(side, side)
		gtx.Constraints = layout.Exact(box)
		icon.Layout(gtx)
		if uxhIconBadged(key) {
			b := side * 4 / 10
			r := max(2, b/5)
			off := image.Pt(side-b, side-b)
			paint.FillShape(gtx.Ops, nativeSurface, clip.UniformRRect(image.Rectangle{Min: off.Sub(image.Pt(2, 2)), Max: box}, r+2).Op(gtx.Ops))
			paint.FillShape(gtx.Ops, nativeInk, clip.UniformRRect(image.Rectangle{Min: off, Max: box}, r).Op(gtx.Ops))
			badge := gtx
			badge.Constraints = layout.Exact(image.Pt(b, b))
			at := op.Offset(off).Push(gtx.Ops)
			layout.Center.Layout(badge, u.monoStyle(unit.Sp(float32(size)*0.17), ">_", nativeSurface, font.Bold))
			at.Pop()
		}
		return layout.Dimensions{Size: box}
	}
}
