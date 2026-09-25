package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestClaudeDesktopModelFilterMatchesBrowser(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required to compare the browser model filter")
	}
	ids := []string{
		"anthropic/claude-sonnet-4.6", "claude-opus-4-6", "claude-Model_1:beta",
		"openai/gpt-5", "vendor/claude-sonnet-4.6", "anthropic/claude-", "claude-",
		"claude-/other", "claude-a/other", "claude-~model", "claude-a+variant",
		"claude-ä", "claude-a\n", "claude-a\x00", "CLAUDE-sonnet-4.6", "", "anthropic/Claude-sonnet-4.6",
		"claude-kilo-v1-deadbeef", "anthropic/claude-kilo-v1-deadbeef", "openai/claude-kilo-v1-model", "~provider/a+variant",
	}
	for _, fragment := range []string{"gpt-5", "GPT-5", "gemini", "GLM", "Kimi", "openai", "phi4", "k2.5", "m2.5", "ling", "unic", "ds-coder", "qwen", "sonnet", "opus", "haiku"} {
		ids = append(ids, "claude-"+fragment, "anthropic/claude-a_"+fragment, "claude-a-"+fragment+"-b")
	}
	for _, size := range []int{199, 200, 201, 255, 256} {
		ids = append(ids, "claude-"+strings.Repeat("x", size-len("claude-")))
	}
	// Exercise every ASCII delimiter both after the family prefix and inside
	// an otherwise valid suffix, including whitespace and JSON escapes.
	for char := rune(0); char < 128; char++ {
		ids = append(ids, "claude-"+string(char)+"x", "anthropic/claude-a"+string(char)+"b")
	}
	data, _ := json.Marshal(ids)
	cmd := exec.Command(node, "--input-type=module", "-e", `import {claudeDesktopModelSupported} from './ui/claude-desktop-helper.mjs';let raw='';for await(const part of process.stdin)raw+=part;process.stdout.write(JSON.stringify(JSON.parse(raw).map(claudeDesktopModelSupported)));`)
	cmd.Stdin = bytes.NewReader(data)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser filter failed: %v %s", err, output)
	}
	var supported []bool
	if err := json.Unmarshal(output, &supported); err != nil || len(supported) != len(ids) {
		t.Fatalf("invalid browser filter results: %v", err)
	}
	for i, id := range ids {
		if got := claudeDesktopModelSupported(id); got != supported[i] {
			t.Fatalf("filter differs for %q: Go=%v, browser=%v", id, got, supported[i])
		}
	}
	cmd = exec.Command(node, "--input-type=module", "-e", `import {claudeDesktopModelAllowed} from './ui/claude-desktop-helper.mjs';let raw='';for await(const part of process.stdin)raw+=part;process.stdout.write(JSON.stringify(JSON.parse(raw).map(id=>claudeDesktopModelAllowed(id,true))));`)
	cmd.Stdin = bytes.NewReader(data)
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("experimental browser filter failed: %v %s", err, output)
	}
	if err := json.Unmarshal(output, &supported); err != nil || len(supported) != len(ids) {
		t.Fatalf("invalid experimental browser results: %v", err)
	}
	for i, id := range ids {
		selection := editorSelection{Models: []editorModel{{ID: id, Name: "Model"}}, Initial: id}
		if got := validateClaudeDesktopSelectionMode(selection, true) == nil; got != supported[i] {
			t.Fatalf("experimental filter differs for %q: Go=%v, browser=%v", id, got, supported[i])
		}
	}
}
