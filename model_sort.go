package main

import (
	"cmp"
	"math"
	"strings"
)

var modelSortOrders = []string{"codeModeRank", "codingIndex", "speed", "price", "name"}

func modelSortOrder(order string) string {
	for _, valid := range modelSortOrders {
		if order == valid {
			return order
		}
	}
	return "codeModeRank"
}

func modelSortValue(m modelInfo, order string) *float64 {
	var value *float64
	switch order {
	case "codeModeRank":
		value = m.CodeModeRank
	case "codingIndex":
		value = m.CodingIndex
	case "speed":
		value = m.Speed
	case "price":
		value = m.InputPrice
	}
	if value == nil || *value < 0 || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return nil
	}
	if order == "codeModeRank" && (*value < 1 || math.Trunc(*value) != *value) {
		return nil
	}
	return value
}

// Display order never changes a profile's selection order or initial model.
// Missing metrics sort last; an explicit zero price or score remains a value.
func compareModelOrder(a, b modelInfo, order, aName, bName string) int {
	order = modelSortOrder(order)
	if order != "name" {
		av, bv := modelSortValue(a, order), modelSortValue(b, order)
		if av == nil && bv != nil {
			return 1
		}
		if av != nil && bv == nil {
			return -1
		}
		if av != nil && bv != nil {
			comparison := cmp.Compare(*av, *bv)
			if order == "codingIndex" || order == "speed" {
				comparison = -comparison
			}
			if comparison != 0 {
				return comparison
			}
		}
	}
	name := func(m modelInfo, display string) string {
		if strings.TrimSpace(display) == "" {
			display = m.Name
		}
		if strings.TrimSpace(display) == "" {
			display = m.ID
		}
		return strings.ToLower(strings.TrimSpace(display))
	}
	if comparison := cmp.Compare(name(a, aName), name(b, bName)); comparison != 0 {
		return comparison
	}
	return cmp.Compare(a.ID, b.ID)
}
