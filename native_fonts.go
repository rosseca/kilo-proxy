//go:build desktop

package main

import (
	"embed"
	"sync"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
)

// Geist and Geist Mono 1.7.2, unmodified, distributed under the SIL Open Font
// License in desktop_assets/LICENSE.txt. The typefaces are embedded; nothing is
// installed.
//
//go:embed desktop_assets/*.otf
var nativeFontAssets embed.FS
var nativeFontsOnce sync.Once
var nativeFontFaces []font.FontFace

// Geist sets body text; Geist Mono sets titles, headings and labels.
const (
	nativeSansFace font.Typeface = "Geist"
	nativeMonoFace font.Typeface = "Geist Mono"
)

func nativeFonts() []font.FontFace {
	nativeFontsOnce.Do(func() {
		for _, name := range []string{
			"Geist-Regular.otf", "Geist-Medium.otf", "Geist-SemiBold.otf", "Geist-Bold.otf",
			"GeistMono-Regular.otf", "GeistMono-Medium.otf", "GeistMono-SemiBold.otf", "GeistMono-Bold.otf",
		} {
			data, err := nativeFontAssets.ReadFile("desktop_assets/" + name)
			if err != nil {
				panic(err)
			}
			faces, err := opentype.ParseCollection(data)
			if err != nil {
				panic(err)
			}
			nativeFontFaces = append(nativeFontFaces, faces...)
		}
		nativeFontFaces = append(nativeFontFaces, gofont.Collection()...)
	})
	return nativeFontFaces
}
