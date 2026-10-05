package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClaude55EffortKeysPreservePickerIdentity(t *testing.T) {
	for _, item := range []struct{ id, effort string }{
		{"anthropic/claude-opus-5.5", "claude-opus-5"},
		{"anthropic/claude-sonnet-5.5", "claude-sonnet-5"},
		{"Anthropic/Claude-Opus-5.5-20261005", "claude-opus-5"},
		{"claude-sonnet-5.5-20261005", "claude-sonnet-5"},
		{"anthropic/claude-opus-5-5", "claude-opus-5-5"},
		{"anthropic/claude-sonnet-5-5-20261005", "claude-sonnet-5-5"},
	} {
		if got := claudeEffortKey(item.id); got != item.effort {
			t.Fatalf("%s effort key = %s, want %s", item.id, got, item.effort)
		}
		if got := claudePickerKey(item.id); got != "" {
			t.Fatalf("new effort support changed existing exact picker identity: %s", got)
		}
	}
	for _, id := range []string{"anthropic/claude-opus-5.55", "other/claude-opus-5.5", "anthropic/claude-haiku-5.5", "anthropic/claude-opus-5.5-extra"} {
		if claudeEffortKey(id) != "" || validClaudeEffort(id, "high") {
			t.Fatalf("invented native family accepted: %s", id)
		}
	}
	if claudePickerKey("anthropic/claude-opus-5") != "claude-opus-5" || claudePickerKey("anthropic/claude-opus-4.6") != "claude-opus-4-6" {
		t.Fatal("existing standalone picker identities changed")
	}
}

func TestClaude55DefaultsRequireVerifiedCLI(t *testing.T) {
	for _, version := range []string{"", "garbage", "2.1.250", "2.1.251", "2.1.266"} {
		caps := claudeCaps(version)
		if claudeEffortCompatible("anthropic/claude-opus-5.5", caps) {
			t.Fatalf("unverified 5.5 canonicalizer accepted: %s", version)
		}
		selection := claudeSelection{Models: []claudeModel{{ID: "anthropic/claude-opus-5.5", Effort: "high"}}, Initial: "anthropic/claude-opus-5.5"}
		if _, err := mergeClaudeSettings(nil, selection, caps, 8877, "fixture"); err == nil || !strings.Contains(err.Error(), "2.1.267") {
			t.Fatalf("old CLI must reject saved 5.5 effort: %s %v", version, err)
		}
		selection.Models[0].Effort = ""
		if _, err := mergeClaudeSettings(nil, selection, caps, 8877, "fixture"); err != nil {
			t.Fatalf("automatic 5.5 should remain available: %s %v", version, err)
		}
	}
	for _, version := range []string{"2.1.267", "2.1.288", "2.1.289 (Claude Code)", "2.2.0", "3.0.0"} {
		if !claudeEffortCompatible("anthropic/claude-opus-5.5", claudeCaps(version)) {
			t.Fatalf("compatible CLI rejected: %s", version)
		}
	}
	if !claudeEffortCompatible("anthropic/claude-opus-5", claudeCaps("2.1.251")) {
		t.Fatal("existing native efforts acquired a stricter minimum")
	}
}

func TestClaude55ManagedDefaultsKeepExactGatewayIDs(t *testing.T) {
	for _, picker := range []bool{false, true} {
		caps := claudeCaps("2.1.288")
		caps.Picker = picker
		selection := claudeSelection{Models: []claudeModel{{ID: "anthropic/claude-opus-5.5", Effort: "high"}, {ID: "anthropic/claude-sonnet-5.5", Effort: "low"}}, Initial: "anthropic/claude-opus-5.5"}
		data, err := mergeClaudeSettings(nil, selection, caps, 8877, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		var settings map[string]any
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatal(err)
		}
		if settings["model"] != selection.Initial || settings["modelOverrides"] != nil || settings["effortLevel"] != nil {
			t.Fatalf("gateway identity or global effort changed: %s", data)
		}
		defaults := object(settings["modelSettings"])
		if len(defaults) != 2 || object(defaults["claude-opus-5"])["effortLevel"] != "high" || object(defaults["claude-sonnet-5"])["effortLevel"] != "low" {
			t.Fatalf("incorrect native defaults: %s", data)
		}
	}
}

func TestClaude55RejectsContradictorySharedNativeKeys(t *testing.T) {
	selection := claudeSelection{Models: []claudeModel{{ID: "anthropic/claude-opus-5.5", Effort: "high"}, {ID: "anthropic/claude-opus-5", Effort: "low"}}, Initial: "anthropic/claude-opus-5.5"}
	for _, effort := range []string{"low", ""} {
		selection.Models[1].Effort = effort
		if _, err := mergeClaudeSettings(nil, selection, claudeCaps("2.1.288"), 8877, "fixture"); err == nil || !strings.Contains(err.Error(), "share a native Claude effort key") {
			t.Fatalf("shared key could silently overwrite a default: %v", err)
		}
	}
	selection.Models[1].Effort = "high"
	if _, err := mergeClaudeSettings(nil, selection, claudeCaps("2.1.288"), 8877, "fixture"); err != nil {
		t.Fatalf("matching defaults should share the native key safely: %v", err)
	}
}
