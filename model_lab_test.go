package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"
	"time"
)

func TestModelLabUsesPublisherWithoutChangingModelIdentity(t *testing.T) {
	for _, test := range []struct {
		name  string
		model modelInfo
		want  string
	}{
		{"explicit publisher wins", modelInfo{ID: "openai/model", Provider: "Anthropic"}, "anthropic"},
		{"normalized publisher", modelInfo{ID: "routing/model", Provider: " \t~~~OpenAI\n"}, "openai"},
		{"missing publisher", modelInfo{ID: "~Google/model"}, "google"},
		{"blank publisher", modelInfo{ID: "~~DeepSeek/model", Provider: "\t \n"}, "deepseek"},
		{"nested route uses first segment", modelInfo{ID: "Qwen/model/variant"}, "qwen"},
		{"manual ID without namespace", modelInfo{ID: "Custom-Model"}, "custom-model"},
		{"manual ID with namespace", modelInfo{ID: "private_lab/custom-model"}, "private_lab"},
		{"missing identity", modelInfo{}, ""},
		{"empty namespace", modelInfo{ID: "/model"}, ""},
		{"empty normalized explicit publisher", modelInfo{ID: "openai/model", Provider: " ~~~ "}, ""},
		{"internal tilde is significant", modelInfo{ID: "routing/model", Provider: "Lab~One"}, "lab~one"},
		{"aliases retain separate namespaces", modelInfo{ID: "~Meta-Llama/model"}, "meta-llama"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := test.model
			if got := modelLab(test.model); got != test.want {
				t.Errorf("modelLab(%+v) = %q, want %q", test.model, got, test.want)
			}
			if !reflect.DeepEqual(test.model, before) {
				t.Fatal("lab normalization changed routing identity")
			}
		})
	}
}

func TestModelLabOptionsKnownNamesAndAliasTieBreaks(t *testing.T) {
	// Labels can match without merging independent publisher namespaces.
	want := []modelLabOption{
		{"", "All labs"},
		{"anthropic", "Anthropic"},
		{"deepseek", "DeepSeek"},
		{"google", "Google"},
		{"meta", "Meta"},
		{"meta-llama", "Meta"},
		{"minimax", "MiniMax"},
		{"mistral", "Mistral AI"},
		{"mistralai", "Mistral AI"},
		{"moonshot", "Moonshot AI"},
		{"moonshotai", "Moonshot AI"},
		{"openai", "OpenAI"},
		{"qwen", "Qwen"},
		{"x-ai", "xAI"},
		{"xai", "xAI"},
		{"z-ai", "Z.ai"},
		{"zai", "Z.ai"},
	}
	var models []modelInfo
	for i := len(want) - 1; i > 0; i-- {
		models = append(models, modelInfo{ID: want[i].Value + "/model"})
	}
	models = append(models,
		modelInfo{ID: "openai/another"},
		modelInfo{ID: "~openai/variant"},
		modelInfo{ID: "route/provider-model", Provider: " ~OPENAI "},
		modelInfo{},
	)
	if got := modelLabOptions(models, "en"); !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog labels/order: got %+v, want %+v", got, want)
	}
	want[0].Label = "Todos los laboratorios"
	if got := modelLabOptions(models, "es"); !reflect.DeepEqual(got, want) {
		t.Fatalf("Spanish labels/order: got %+v, want %+v", got, want)
	}
}

func TestModelLabOptionsAreDynamicAndDoNotRequireCompleteMetadata(t *testing.T) {
	models := []modelInfo{
		{ID: "manual-only"},
		{ID: "constructor/model"},
		{ID: "__proto__/model"},
		{ID: "___/model"},
		{ID: "---/model"},
		{ID: "lab.v2/model"},
		{ID: "route/new", Provider: " ACME__research--lab "},
		{ID: "acme__research--lab/second"},
		{ID: "café-labs/third"},
		{ID: "实验-lab/fourth"},
		{},
	}
	want := []modelLabOption{
		{"", "All labs"},
		{"---", "---"},
		{"___", "___"},
		{"acme__research--lab", "Acme Research Lab"},
		{"café-labs", "Café Labs"},
		{"constructor", "Constructor"},
		{"lab.v2", "Lab.V2"},
		{"manual-only", "Manual Only"},
		{"__proto__", "Proto"},
		{"实验-lab", "实验 Lab"},
	}
	if got := modelLabOptions(models, "en"); !reflect.DeepEqual(got, want) {
		t.Fatalf("dynamic labels: got %+v, want %+v", got, want)
	}
	// A refreshed catalog must remove obsolete labs and discover new ones.
	refreshed := []modelInfo{{ID: "new_vendor/model"}}
	if got, want := modelLabOptions(refreshed, "en"), []modelLabOption{{"", "All labs"}, {"new_vendor", "New Vendor"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("refreshed labels: got %+v, want %+v", got, want)
	}
	for _, language := range []string{"en", "", "fr", "es-ES"} {
		if got, want := modelLabOptions(nil, language), []modelLabOption{{"", "All labs"}}; !reflect.DeepEqual(got, want) {
			t.Errorf("empty catalog, language %q: got %+v, want %+v", language, got, want)
		}
	}
}

func TestFilterModelLabPreservesCatalogOrderAndModelConfiguration(t *testing.T) {
	price, tools := 0.75, true
	models := []modelInfo{
		{ID: "~openai/second", Name: "Custom display", Tools: &tools, InputPrice: &price, ReasoningEfforts: []string{"low", "high"}},
		{ID: "anthropic/model", Name: "Other"},
		{ID: "openai/first", Provider: "OPENAI"},
		{ID: "manual-model"},
		{ID: "meta/model"},
		{ID: "meta-llama/model"},
	}
	before := append([]modelInfo(nil), models...)
	for _, lab := range []string{"openai", " ~~~OpenAI\t"} {
		if got, want := filterModelLab(models, lab), []modelInfo{models[0], models[2]}; !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %+v, want %+v", lab, got, want)
		}
	}
	for _, lab := range []string{"", "  ", "~~~"} {
		got := filterModelLab(models, lab)
		if !reflect.DeepEqual(got, models) {
			t.Errorf("all labs %q changed model order/content: %+v", lab, got)
		}
		got[0].ID = "changed-view"
		if models[0].ID != before[0].ID {
			t.Fatal("filtered view shares the source slice storage")
		}
	}
	for lab, index := range map[string]int{"manual-model": 3, "meta": 4, "meta-llama": 5} {
		if got, want := filterModelLab(models, lab), []modelInfo{models[index]}; !reflect.DeepEqual(got, want) {
			t.Errorf("%q merged distinct namespaces: got %+v, want %+v", lab, got, want)
		}
	}
	if got := filterModelLab(models, "unavailable"); len(got) != 0 {
		t.Fatalf("unknown lab unexpectedly matched %+v", got)
	}
	if got := filterModelLab(nil, ""); len(got) != 0 {
		t.Fatalf("empty input unexpectedly matched %+v", got)
	}
	if !reflect.DeepEqual(models, before) {
		t.Fatal("filter changed source models")
	}
}

func TestNativeAndBrowserModelLabsAgree(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for browser/native parity")
	}
	models := []modelInfo{
		{ID: "openai/a", Name: "A", Provider: "\t~~~OpenAI "},
		{ID: "~openai/b", Name: "B"},
		{ID: "anthropic/c", Provider: " \n"},
		{ID: "routing/d", Provider: "Google"},
		{ID: "moonshotai/e"}, {ID: "moonshot/e"},
		{ID: "meta-llama/f"}, {ID: "meta/f"},
		{ID: "xai/g"}, {ID: "x-ai/g"},
		{ID: "zai/h"}, {ID: "z-ai/h"},
		{ID: "mistralai/i"}, {ID: "mistral/i"},
		{ID: "minimax/j"}, {ID: "qwen/j"}, {ID: "deepseek/j"},
		{ID: "acme__research--lab/k"}, {ID: "café-labs/k"},
		{ID: "实验-lab/k"}, {ID: "lab.v2/l"}, {ID: "lab~one/m"},
		{ID: "constructor/n"}, {ID: "__proto__/n"},
		{ID: "___/n"}, {ID: "---/n"},
		{ID: "manual-model"}, {ID: "google/no-fallback", Provider: "~~~"},
		{ID: "/empty-namespace"}, {},
	}
	languages := []string{"en", "es", "", "es-ES"}
	filters := []string{"", " \t", "~~~", "openai", " ~~~OpenAI ", "google", "meta", "meta-llama", "manual-model", "café-labs", "missing"}
	input, err := json.Marshal(map[string]any{"models": models, "languages": languages, "filters": filters})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--input-type=module", "-e", `
import {modelLab,modelLabOptions,filterModelLab} from './ui/model-helper.mjs';
let input=''; for await (const chunk of process.stdin) input+=chunk;
const {models,languages,filters}=JSON.parse(input);
process.stdout.write(JSON.stringify({
  labs:models.map(modelLab),
  options:Object.fromEntries(languages.map(language=>[language,modelLabOptions(models,language)])),
  filtered:Object.fromEntries(filters.map(lab=>[lab,filterModelLab(models,lab)]))
}));`)
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser lab helpers: %v\n%s", err, output)
	}
	var browser struct {
		Labs     []string                    `json:"labs"`
		Options  map[string][]modelLabOption `json:"options"`
		Filtered map[string][]modelInfo      `json:"filtered"`
	}
	if err := json.Unmarshal(output, &browser); err != nil {
		t.Fatalf("invalid browser lab result: %v\n%s", err, output)
	}
	for i, model := range models {
		if got := modelLab(model); i >= len(browser.Labs) || got != browser.Labs[i] {
			t.Errorf("model %d native lab %q differs from browser result %v", i, got, browser.Labs)
		}
	}
	for _, language := range languages {
		if got := modelLabOptions(models, language); !reflect.DeepEqual(got, browser.Options[language]) {
			t.Errorf("language %q options differ: native=%+v browser=%+v", language, got, browser.Options[language])
		}
	}
	for _, lab := range filters {
		if got := filterModelLab(models, lab); !reflect.DeepEqual(got, browser.Filtered[lab]) {
			t.Errorf("lab %q filter differs: native=%+v browser=%+v", lab, got, browser.Filtered[lab])
		}
	}
}
