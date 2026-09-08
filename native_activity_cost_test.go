//go:build desktop

package main

import (
	"image"
	"strings"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

func TestNativeReportedSpendPartialZeroIsNotTotal(t *testing.T) {
	for _, language := range []string{"en", "es"} {
		u := &nativeUI{language: language}
		summary := usageSummary{Requests: 38, Priced: 2, Incomplete: 19, CostUSD: "0.000000000"}
		label, amount := u.reportedSpend(summary)
		if label != u.tr("Reported subtotal", "Subtotal informado") || amount != "$0" {
			t.Fatalf("%s: partially reported zero looked like a total: %s %s", language, label, amount)
		}
		want := u.tr("Cost reported: 2/38 requests\n36 requests without reported cost", "Coste informado: 2/38 peticiones\n36 peticiones sin coste informado")
		if u.coverage(summary) != want {
			t.Fatalf("%s: missing price count incorrect: %s", language, u.coverage(summary))
		}
		want = u.tr("Response stats: 19 interrupted or limited", "Estadísticas de respuesta: 19 interrumpidas o limitadas")
		if u.responseStats(summary) != want {
			t.Fatalf("%s: response completeness was conflated with price coverage", language)
		}
		summary.CostUSD = "0.125000000"
		label, amount = u.reportedSpend(summary)
		if label != u.tr("Reported subtotal", "Subtotal informado") || amount != "$0.125" {
			t.Fatal("known costs were estimated or lost")
		}
	}
}

// Measure real typography without starting an app, HTTP fixture, or GPU window.
func TestNativeReportedSpendCaptionFitsMetric(t *testing.T) {
	for _, language := range []string{"en", "es"} {
		for _, width := range []int{220, 280} {
			theme := material.NewTheme()
			theme.Shaper = text.NewShaper(text.WithCollection(nativeFonts()))
			theme.TextSize, theme.Face = 14, "Inter"
			u := &nativeUI{language: language, theme: theme}
			summary := usageSummary{Requests: 38, Priced: 2, Incomplete: 19, CostUSD: "0.000000000"}
			label, amount := u.reportedSpend(summary)
			var ops op.Ops
			gtx := layout.Context{Ops: &ops, Constraints: layout.Constraints{Min: image.Pt(width, 0), Max: image.Pt(width, 1000)}, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
			dims := u.metric(label, amount, u.coverage(summary))(gtx)
			if dims.Size.X > width || dims.Size.Y > 130 {
				t.Fatalf("%s width %d: cost metric is clipped or overly tall: %v", language, width, dims.Size)
			}
			full := summary
			full.Priced = full.Requests
			fullLabel, fullAmount := u.reportedSpend(full)
			fullDims := u.metric(fullLabel, fullAmount, u.coverage(full))(gtx)
			if fullDims.Size.X > width || fullDims.Size.Y > 130 {
				t.Fatalf("fully reported inference cost label does not fit: %v", fullDims.Size)
			}
			firstText := strings.SplitN(u.coverage(summary), "\n", 2)[0]
			first := u.note(firstText)(gtx)
			// Font line advance can be smaller than a glyph bounding box:
			// Inter has 14px for one line and 27px for two at this scale.
			// Compare to real two-line typography, not twice a rounded box.
			twoLines := u.note(firstText + "\n" + firstText)(gtx)
			caption := u.note(u.coverage(summary))(gtx)
			if first.Size.Y < 10 || twoLines.Size.Y <= first.Size.Y || caption.Size.Y < twoLines.Size.Y {
				t.Fatalf("cost coverage lines were lost: first %v, reference %v, actual %v", first.Size, twoLines.Size, caption.Size)
			}
		}
	}
}

func TestNativeInferenceCostSourceSemantics(t *testing.T) {
	for _, language := range []string{"en", "es"} {
		u := &nativeUI{language: language}
		for source, want := range map[string]string{
			"usage.cost_details.upstream_inference_cost":    u.tr("Provider inference", "Inferencia del proveedor"),
			"provider_metadata.gateway.marketCost":          u.tr("Gateway market cost", "Coste de mercado del gateway"),
			"response.provider_metadata.gateway.marketCost": u.tr("Gateway market cost", "Coste de mercado del gateway"),
			"usage.cost":              u.tr("Gateway-reported cost", "Coste informado por el gateway"),
			"usage.cost_microdollars": u.tr("Gateway-reported cost", "Coste informado por el gateway"),
			"":                        "", "unknown.field": "",
		} {
			if got := u.costSourceLabel(source); got != want {
				t.Fatalf("%s %s: %q != %q", language, source, got, want)
			}
		}
		note := u.inferenceCostNote()
		for _, part := range []string{"BYOK", u.tr("may differ from your Kilo organization’s charges", "Pueden diferir de los cargos"), u.tr("not estimates", "no son estimaciones")} {
			if !strings.Contains(note, part) {
				t.Fatalf("inference cost note omitted %q: %s", part, note)
			}
		}
	}
}

func TestNativeReportedSpendAllZeroAndUnknown(t *testing.T) {
	for _, language := range []string{"en", "es"} {
		u := &nativeUI{language: language}
		summary := usageSummary{Requests: 38, Priced: 38, Incomplete: 19, CostUSD: "0.000000000"}
		label, amount := u.reportedSpend(summary)
		if label != u.tr("Reported inference cost", "Coste de inferencia informado") || amount != "$0" {
			t.Fatal("response incompleteness invalidated explicitly reported zero")
		}
		if !strings.Contains(u.coverage(summary), u.tr("0 requests without reported cost", "0 peticiones sin coste informado")) {
			t.Fatal("incomplete response count was used as missing price count")
		}
		for _, requests := range []int64{0, 38} {
			summary.Requests, summary.Priced = requests, 0
			_, amount = u.reportedSpend(summary)
			if amount != "—" {
				t.Fatalf("%s: %d unpriced requests displayed a zero cost", language, requests)
			}
		}
	}
}
