package main

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestTerminalEncodedArgumentsPreserveNativeValues(t *testing.T) {
	for _, args := range [][]string{{}, {""}, {"run", "quotes \" '", "trailing\\", "$(not-evaluated); & | %PATH%", "line\nbreak", "日本語 😀", "--config-dir", "client-value"}} {
		data, _ := json.Marshal(args)
		got, err := decodeTerminalArguments(base64.StdEncoding.EncodeToString(data))
		if err != nil || !reflect.DeepEqual(got, args) {
			t.Fatalf("changed argument array: %q %v", got, err)
		}
	}
}

func TestTerminalEncodedArgumentsRejectMalformedInput(t *testing.T) {
	for _, raw := range []string{"null", "{}", "true", `"argument"`, `[null]`, `[1]`, `["nul\u0000"]`, `["one"] ["two"]`, string([]byte{'[', '"', 0xff, '"', ']'})} {
		if _, err := decodeTerminalArguments(base64.StdEncoding.EncodeToString([]byte(raw))); err == nil {
			t.Fatalf("accepted malformed argument array %q", raw)
		}
	}
	for _, encoded := range []string{"", "not-base64!", strings.Repeat("A", (1<<20)+1)} {
		if _, err := decodeTerminalArguments(encoded); err == nil {
			t.Fatal("accepted invalid encoded input")
		}
	}
}
