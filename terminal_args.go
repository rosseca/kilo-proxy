package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// Windows PowerShell 5.1 cannot faithfully pass every native argument (notably
// empty strings and embedded quotes). The generated functions encode one JSON
// string array instead; decode it before appending to the actual client's argv.
func decodeTerminalArguments(encoded string) ([]string, error) {
	invalid := errors.New("Invalid encoded terminal arguments")
	if encoded == "" || len(encoded) > 1<<20 {
		return nil, invalid
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || !utf8.Valid(data) {
		return nil, invalid
	}
	var values []json.RawMessage
	if json.Unmarshal(data, &values) != nil || values == nil {
		return nil, invalid
	}
	args := make([]string, len(values))
	for i, value := range values {
		if len(value) == 0 || value[0] != '"' || json.Unmarshal(value, &args[i]) != nil || strings.ContainsRune(args[i], '\x00') {
			return nil, invalid
		}
	}
	return args, nil
}
