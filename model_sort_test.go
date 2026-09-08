package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os/exec"
	"reflect"
	"sort"
	"testing"
)

func modelSortFixture() ([]modelInfo, map[string]string) {
	value := func(n float64) *float64 { return &n }
	return []modelInfo{
		{ID: "vendor/b", Name: "Beta", CodeModeRank: value(1), CodingIndex: value(20), Speed: value(0), InputPrice: value(0)},
		{ID: "vendor/z", Name: "Zulu", CodeModeRank: value(2), CodingIndex: value(60), Speed: value(200), InputPrice: value(3)},
		{ID: "vendor/a", Name: "ALPHA", CodeModeRank: value(2), CodingIndex: value(60), Speed: value(200), InputPrice: value(1)},
		{ID: "vendor/missing", Name: "A missing metric"},
	}, map[string]string{"vendor/z": "Alpha"}
}

func sortedModelIDs(models []modelInfo, names map[string]string, order string) []string {
	view := append([]modelInfo(nil), models...)
	sort.Slice(view, func(i, j int) bool {
		return compareModelOrder(view[i], view[j], order, names[view[i].ID], names[view[j].ID]) < 0
	})
	ids := make([]string, len(view))
	for i, model := range view {
		ids[i] = model.ID
	}
	return ids
}

func TestModelSortingUsesPublishedDirectionAndStableTies(t *testing.T) {
	models, names := modelSortFixture()
	for order, want := range map[string][]string{
		"codeModeRank": {"vendor/b", "vendor/a", "vendor/z", "vendor/missing"},
		"codingIndex":  {"vendor/a", "vendor/z", "vendor/b", "vendor/missing"},
		"speed":        {"vendor/a", "vendor/z", "vendor/b", "vendor/missing"},
		"price":        {"vendor/b", "vendor/a", "vendor/z", "vendor/missing"},
		"name":         {"vendor/missing", "vendor/a", "vendor/z", "vendor/b"},
	} {
		if got := sortedModelIDs(models, names, order); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v, want %v", order, got, want)
		}
	}
	if modelSortOrder("") != "codeModeRank" || modelSortOrder("invalid") != "codeModeRank" {
		t.Fatal("default sorting does not use the code-mode ranking")
	}
}

func TestModelSortingDistinguishesMissingAndZero(t *testing.T) {
	for _, order := range modelSortOrders[:4] {
		for _, invalid := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
			zero := 0.0
			known := modelInfo{ID: "known", Name: "Z known", InputPrice: &zero, Speed: &zero, CodingIndex: &zero, CodeModeRank: new(float64)}
			*known.CodeModeRank = 1
			missing := modelInfo{ID: "missing", Name: "A missing", InputPrice: &invalid, Speed: &invalid, CodingIndex: &invalid, CodeModeRank: &invalid}
			if compareModelOrder(known, missing, order, "", "") >= 0 {
				t.Errorf("%s: known zero/rank must precede invalid metric %v", order, invalid)
			}
		}
	}
	for _, invalid := range []float64{0, 1.5} {
		if modelSortValue(modelInfo{CodeModeRank: &invalid}, "codeModeRank") != nil {
			t.Errorf("invalid rank %v accepted", invalid)
		}
	}
}

func TestNativeAndBrowserModelOrderingAgree(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for browser/native parity")
	}
	models, names := modelSortFixture()
	var input []map[string]any
	for _, model := range models {
		encoded, _ := json.Marshal(model)
		var value map[string]any
		_ = json.Unmarshal(encoded, &value)
		value["displayName"] = names[model.ID]
		input = append(input, value)
	}
	data, _ := json.Marshal(input)
	command := exec.Command(node, "--input-type=module", "-e", `
import {sortModels} from './ui/model-helper.mjs';
let data=''; for await (const chunk of process.stdin) data+=chunk;
const models=JSON.parse(data), result={};
for (const order of ['codeModeRank','codingIndex','speed','price','name']) result[order]=sortModels(models,order).map(m=>m.id);
process.stdout.write(JSON.stringify(result));`)
	command.Stdin = bytes.NewReader(data)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser comparator: %v\n%s", err, output)
	}
	var browser map[string][]string
	if err := json.Unmarshal(output, &browser); err != nil {
		t.Fatal(err)
	}
	for _, order := range modelSortOrders {
		if native := sortedModelIDs(models, names, order); !reflect.DeepEqual(native, browser[order]) {
			t.Errorf("%s differs: native=%v browser=%v", order, native, browser[order])
		}
	}
}
