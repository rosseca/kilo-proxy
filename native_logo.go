//go:build desktop

package main

import (
	"bytes"
	_ "embed"
	"image"
	"image/png"

	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

// The window, tray and package icons share the selected geometric K artwork.
//
//go:embed ui/icon.png
var nativeLogoPNG []byte

var nativeLogo = func() widget.Image {
	img, err := png.Decode(bytes.NewReader(nativeLogoPNG))
	if err != nil {
		panic(err)
	}
	return widget.Image{Src: paint.NewImageOp(img), Fit: widget.Contain}
}()

func nativeLogoLayout(size unit.Dp) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(size), gtx.Dp(size)))
		return nativeLogo.Layout(gtx)
	}
}
