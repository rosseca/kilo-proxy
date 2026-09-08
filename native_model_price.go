//go:build desktop

package main

import (
	"math"
	"strconv"
	"strings"
)

func nativeTokenPrice(value *float64) string {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
		return "—"
	}
	if *value == 0 {
		return "$0"
	}
	// Gateway prices are converted to USD per million tokens. Round only for
	// display, avoiding binary floating-point tails without changing sorting.
	if *value < 0.000001 {
		return "<$0.000001"
	}
	price := strconv.FormatFloat(*value, 'f', 6, 64)
	return "$" + strings.TrimRight(strings.TrimRight(price, "0"), ".")
}

func nativeModelPrice(m modelInfo) string {
	return nativeTokenPrice(m.InputPrice) + " / " + nativeTokenPrice(m.OutputPrice) + " USD / 1M"
}
