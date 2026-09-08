//go:build desktop

package main

import (
	"math"
	"testing"
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
