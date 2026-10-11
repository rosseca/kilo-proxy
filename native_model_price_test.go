//go:build desktop

package main

import (
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"image"
	"math"
	"strings"
	"testing"
	"time"
)

func TestNativeTokenPriceReadableGatewayValues(t *testing.T) {
	cases := []struct {
		name  string
		value *float64
		want  string
	}{
		{"missing", nil, "—"},
		{"free", ptrFloat(0), "$0"},
		{"input conversion", ptrFloat(0.7999999999999999), "$0.8"},
		{"output conversion", ptrFloat(1.5999999999999999), "$1.6"},
		{"integer", ptrFloat(15), "$15"},
		{"fractional", ptrFloat(0.075), "$0.075"},
		{"small", ptrFloat(0.000001), "$0.000001"},
		{"below precision", ptrFloat(0.0000001), "<$0.000001"},
		{"negative", ptrFloat(-1), "—"},
		{"not a number", ptrFloat(math.NaN()), "—"},
		{"infinite", ptrFloat(math.Inf(1)), "—"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nativeTokenPrice(tc.value); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNativeModelPricesSeparateSubscriptionFromKilo(t *testing.T) {
	for _, language := range []string{"en", "es"} {
		t.Run(language, func(t *testing.T) {
			u := nativeTestUI(t)
			u.language = language
			for _, id := range []string{"chatgpt/gpt-5", "openai/gpt-5", "other/chatgpt/gpt-5"} {
				var ops op.Ops
				var router input.Router
				gtx := layout.Context{Ops: &ops, Source: router.Source(), Constraints: layout.Exact(image.Pt(400, 100)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Now: time.Now()}
				// Even stale prices from a former catalog must not turn subscription use into money.
				u.modelPriceCells(modelInfo{ID: id, InputPrice: ptrFloat(0), OutputPrice: ptrFloat(3)})(gtx)
				router.Frame(&ops)
				labels := []string{}
				for _, node := range router.AppendSemantics(nil) {
					labels = append(labels, node.Desc.Label)
				}
				text := strings.Join(labels, "\n")
				if strings.HasPrefix(id, "chatgpt/") {
					if !strings.Contains(text, u.tr("Subscription quota", "Cuota de suscripción")) || strings.Contains(text, "USD") || strings.Contains(text, "$") {
						t.Fatalf("subscription shown as token price: %s", text)
					}
				} else if !strings.Contains(text, "USD / 1M") || !strings.Contains(text, "$0 / $3") {
					t.Fatalf("Kilo published token prices lost: %s", text)
				}
			}
		})
	}
}
