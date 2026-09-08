package brand

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// x/image/vector uses CPU-specific rasterization. Measured x64/ARM64 output
// differs in 27 of 262144 app-icon pixels (at most 2/255 in one channel), six
// transparent-mark pixels and one tray-mask pixel. Keep SVG geometry exact and
// allow only this scale of antialias rounding in decoded, premultiplied pixels.
func compareRaster(a, b image.Image) error {
	if a.Bounds() != b.Bounds() {
		return fmt.Errorf("image dimensions changed")
	}
	changed := 0
	for y := a.Bounds().Min.Y; y < a.Bounds().Max.Y; y++ {
		for x := a.Bounds().Min.X; x < a.Bounds().Max.X; x++ {
			ar, ag, ab, aa := a.At(x, y).RGBA()
			br, bg, bb, ba := b.At(x, y).RGBA()
			different := false
			for i, av := range []uint32{ar, ag, ab, aa} {
				bv := []uint32{br, bg, bb, ba}[i]
				delta := int(av>>8) - int(bv>>8)
				if delta < 0 {
					delta = -delta
				}
				if delta > 2 {
					return fmt.Errorf("pixel (%d,%d) changed by %d/255", x, y, delta)
				}
				different = different || delta > 0
			}
			if different {
				changed++
			}
		}
	}
	limit := max(2, a.Bounds().Dx()*a.Bounds().Dy()/4000)
	if changed > limit {
		return fmt.Errorf("%d pixels changed; antialias budget is %d", changed, limit)
	}
	return nil
}

func TestCheckedInAssetsMatchSelectedArtwork(t *testing.T) {
	for name, want := range Assets() {
		got, err := os.ReadFile(filepath.Join("..", "..", "ui", name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(name, ".png") {
			if !bytes.Equal(got, want) {
				t.Errorf("%s geometry differs; run go run ./cmd/icon-assets", name)
			}
			continue
		}
		actual, err := png.Decode(bytes.NewReader(got))
		if err != nil {
			t.Fatal(err)
		}
		expected, err := png.Decode(bytes.NewReader(want))
		if err != nil {
			t.Fatal(err)
		}
		if err := compareRaster(actual, expected); err != nil {
			t.Errorf("%s differs from the canonical mark: %v", name, err)
		}
	}
}

func TestRasterComparisonRejectsArtworkChanges(t *testing.T) {
	a := image.NewNRGBA(image.Rect(0, 0, 100, 100))
	b := image.NewNRGBA(a.Bounds())
	b.SetNRGBA(10, 10, color.NRGBA{R: 255, A: 255})
	if compareRaster(a, b) == nil {
		t.Fatal("comparison accepted a new opaque shape")
	}
	b = image.NewNRGBA(a.Bounds())
	for x := 0; x < 100; x++ {
		b.SetNRGBA(x, 0, color.NRGBA{A: 1})
	}
	if compareRaster(a, b) == nil {
		t.Fatal("comparison accepted widespread low-amplitude changes")
	}
	b = image.NewNRGBA(a.Bounds())
	b.SetNRGBA(1, 1, color.NRGBA{A: 1})
	if err := compareRaster(a, b); err != nil {
		t.Fatalf("one antialias rounding pixel rejected: %v", err)
	}
}

func TestTransparencyAndTrayMask(t *testing.T) {
	mark := Render(512, "mark")
	for _, p := range []image.Point{{0, 0}, {0, 100}, {511, 100}, {0, 400}, {511, 400}, {256, 260}} {
		if mark.NRGBAAt(p.X, p.Y).A != 0 {
			t.Fatalf("transparent mark has a painted background at %v", p)
		}
	}
	template := Render(44, "template")
	visible := 0
	for y := 0; y < 44; y++ {
		for x := 0; x < 44; x++ {
			p := template.NRGBAAt(x, y)
			if p.R != 0 || p.G != 0 || p.B != 0 {
				t.Fatal("macOS template contains color")
			}
			if p.A != 0 {
				visible++
			}
		}
	}
	if visible < 400 || visible > 1600 {
		t.Fatalf("unusable template silhouette coverage: %d", visible)
	}
}
