// Package brand defines the selected geometric K artwork once for every surface.
package brand

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"

	"golang.org/x/image/vector"
)

type command struct {
	kind   byte
	points []float32
}
type shape []command

func c(kind byte, points ...float32) command { return command{kind, points} }

var stem = shape{
	c('M', 60, 0), c('L', 243, 0), c('Q', 303, 0, 303, 60), c('L', 303, 323),
	c('C', 303, 395, 248, 410, 205, 448), c('C', 184, 470, 171, 488, 171, 517),
	c('C', 171, 562, 206, 591, 239, 615), c('C', 279, 644, 303, 660, 303, 708),
	c('L', 303, 927), c('Q', 303, 987, 243, 987), c('L', 60, 987), c('Q', 0, 987, 0, 927), c('L', 0, 60), c('Q', 0, 0, 60, 0), c('Z'),
}
var upper = shape{
	c('M', 331, 475), c('Q', 303, 475, 303, 447), c('Q', 303, 435, 317, 420), c('L', 684, 37),
	c('Q', 719, 0, 762, 0), c('L', 938, 0), c('Q', 1000, 0, 1000, 67), c('L', 1000, 82),
	c('Q', 1000, 135, 958, 177), c('L', 694, 438), c('Q', 653, 475, 565, 475), c('Z'),
}
var accent = shape{
	c('M', 444, 475), c('Q', 527, 475, 527, 400), c('L', 527, 258), c('Q', 527, 208, 557, 175),
	c('L', 686, 39), c('Q', 723, 0, 763, 0), c('L', 938, 0), c('Q', 1000, 0, 1000, 67), c('L', 1000, 82),
	c('Q', 1000, 135, 958, 177), c('L', 694, 438), c('Q', 653, 475, 565, 475), c('Z'),
}
var lower = shape{
	c('M', 338, 550), c('L', 568, 550), c('Q', 641, 550, 695, 604), c('L', 978, 885),
	c('Q', 1000, 907, 1000, 932), c('Q', 1000, 987, 945, 987), c('L', 735, 987),
	c('Q', 693, 987, 663, 959), c('L', 313, 602), c('Q', 302, 591, 304, 576), c('Q', 307, 550, 338, 550), c('Z'),
}
var tile = shape{
	c('M', 200, 0), c('L', 800, 0), c('Q', 1000, 0, 1000, 200), c('L', 1000, 800), c('Q', 1000, 1000, 800, 1000),
	c('L', 200, 1000), c('Q', 0, 1000, 0, 800), c('L', 0, 200), c('Q', 0, 0, 200, 0), c('Z'),
}
var forest = color.NRGBA{0x20, 0x27, 0x20, 255}
var citrus = color.NRGBA{0xe8, 0xf3, 0x6a, 255}
var ivory = color.NRGBA{0xf4, 0xf5, 0xef, 255}

type layer struct {
	path        shape
	fill        color.NRGBA
	scale, x, y float32
}

func layers(variant string) []layer {
	scale, x, y := float32(.944), float32(28), float32(34.136)
	colors := []color.NRGBA{forest, forest, forest, citrus}
	var result []layer
	if variant == "icon" || variant == "tray" {
		result = append(result, layer{tile, ivory, 1, 0, 0})
		scale, x, y = .7, 150, 154.55
	} else if variant == "template" {
		scale, x, y = .856, 72, 77.564
		for i := range colors {
			colors[i] = color.NRGBA{A: 255}
		}
	}
	for i, p := range []shape{stem, upper, lower, accent} {
		result = append(result, layer{p, colors[i], scale, x, y})
	}
	return result
}

func Render(size int, variant string) *image.NRGBA {
	result := image.NewRGBA(image.Rect(0, 0, size, size))
	unit := float32(size) / 1000
	for _, layer := range layers(variant) {
		r := vector.NewRasterizer(size, size)
		point := func(p []float32, i int) (float32, float32) {
			return (p[i]*layer.scale + layer.x) * unit, (p[i+1]*layer.scale + layer.y) * unit
		}
		for _, cmd := range layer.path {
			switch cmd.kind {
			case 'M':
				x, y := point(cmd.points, 0)
				r.MoveTo(x, y)
			case 'L':
				x, y := point(cmd.points, 0)
				r.LineTo(x, y)
			case 'Q':
				x1, y1 := point(cmd.points, 0)
				x2, y2 := point(cmd.points, 2)
				r.QuadTo(x1, y1, x2, y2)
			case 'C':
				x1, y1 := point(cmd.points, 0)
				x2, y2 := point(cmd.points, 2)
				x3, y3 := point(cmd.points, 4)
				r.CubeTo(x1, y1, x2, y2, x3, y3)
			case 'Z':
				r.ClosePath()
			}
		}
		r.DrawOp = draw.Over
		r.Draw(result, result.Bounds(), image.NewUniform(layer.fill), image.Point{})
	}
	out := image.NewNRGBA(result.Bounds())
	draw.Draw(out, out.Bounds(), result, image.Point{}, draw.Src)
	return out
}

func PNG(size int, variant string) []byte {
	var out bytes.Buffer
	if err := png.Encode(&out, Render(size, variant)); err != nil {
		panic(err)
	}
	return out.Bytes()
}

func SVG(variant string) []byte {
	var out strings.Builder
	out.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1000 1000" role="img" aria-label="Kilo Proxy">` + "\n")
	for _, layer := range layers(variant) {
		fmt.Fprintf(&out, `  <path fill="#%02x%02x%02x" transform="translate(%g %g) scale(%g)" d="`, layer.fill.R, layer.fill.G, layer.fill.B, layer.x, layer.y, layer.scale)
		for _, cmd := range layer.path {
			out.WriteByte(cmd.kind)
			for i, p := range cmd.points {
				if i > 0 {
					out.WriteByte(' ')
				}
				fmt.Fprintf(&out, "%g", p)
			}
			out.WriteByte(' ')
		}
		out.WriteString("\"/>\n")
	}
	out.WriteString("</svg>\n")
	return []byte(out.String())
}

func Assets() map[string][]byte {
	return map[string][]byte{
		"icon.svg": SVG("icon"), "icon.png": PNG(512, "icon"),
		"logo-mark.svg": SVG("mark"), "logo-mark.png": PNG(512, "mark"),
		"tray.png": PNG(44, "tray"), "tray-template.png": PNG(44, "template"),
	}
}
