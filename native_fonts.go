//go:build desktop

package main

import (
	"embed"
	"sync"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
)

// Inter 4.1, unmodified, distributed under the SIL Open Font License in
// desktop_assets/LICENSE.txt. The typeface is embedded; nothing is installed.
//
//go:embed desktop_assets/*.otf
var nativeFontAssets embed.FS
var nativeFontsOnce sync.Once
var nativeFontFaces []font.FontFace

func nativeFonts() []font.FontFace {
	nativeFontsOnce.Do(func() {
		for _, name := range []string{"Inter-Regular.otf", "Inter-Medium.otf", "Inter-SemiBold.otf"} {
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
