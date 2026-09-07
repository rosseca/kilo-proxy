package brand

import (
	"bytes"
	"image"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckedInAssetsMatchSelectedArtwork(t *testing.T) {
	for name, want := range Assets() {
		got, err := os.ReadFile(filepath.Join("..", "..", "ui", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from the canonical mark; run go run ./cmd/icon-assets", name)
		}
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
