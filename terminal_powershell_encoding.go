package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var errTerminalPowerShellEncoding = errors.New("The PowerShell profile encoding is invalid; nothing was changed.")

func terminalPowerShellProfileContent(old []byte, functions string) ([]byte, error) {
	data := old
	var bom []byte
	var order binary.ByteOrder
	switch {
	case bytes.HasPrefix(old, []byte{0xff, 0xfe, 0, 0}), bytes.HasPrefix(old, []byte{0, 0, 0xfe, 0xff}):
		return nil, errTerminalPowerShellEncoding
	case bytes.HasPrefix(old, []byte{0xff, 0xfe}):
		bom, data, order = old[:2], old[2:], binary.LittleEndian
	case bytes.HasPrefix(old, []byte{0xfe, 0xff}):
		bom, data, order = old[:2], old[2:], binary.BigEndian
	case bytes.HasPrefix(old, []byte{0xef, 0xbb, 0xbf}):
		bom, data = old[:3], old[3:]
		if !utf8.Valid(data) {
			return nil, errTerminalPowerShellEncoding
		}
	case len(old) == 0:
		bom = []byte{0xef, 0xbb, 0xbf}
	}
	if order != nil {
		if len(data)%2 != 0 {
			return nil, errTerminalPowerShellEncoding
		}
		units := make([]uint16, len(data)/2)
		for i := range units {
			units[i] = order.Uint16(data[i*2:])
		}
		for i := 0; i < len(units); i++ {
			if units[i] >= 0xd800 && units[i] <= 0xdbff {
				if i+1 >= len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
					return nil, errTerminalPowerShellEncoding
				}
				i++
			} else if units[i] >= 0xdc00 && units[i] <= 0xdfff {
				return nil, errTerminalPowerShellEncoding
			}
		}
		data = []byte(string(utf16.Decode(units)))
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, errTerminalPowerShellEncoding
	}
	if bytes.Contains(data, []byte("# SIG # Begin signature block")) {
		return nil, errors.New("This PowerShell profile is signed. Add the commands manually and sign it again.")
	}
	// Remove only a well-formed owned block before checking for user functions
	// or aliases that would otherwise be silently overridden by installation.
	unmanaged, err := terminalStartupContent(data, nil, false)
	if err != nil {
		return nil, errors.New("The Kilo Proxy PowerShell block is incomplete or duplicated. Repair that block before installing terminal commands.")
	}
	if terminalPowerShellCommandConflict.Match(unmanaged) {
		return nil, errors.New("An existing PowerShell command uses a Kilo command name. Rename it before installing terminal commands.")
	}
	block := terminalPathBegin + "\n" + functions + terminalPathEnd + "\n"
	newline := "\r\n"
	if bytes.Contains(data, []byte("\n")) && !bytes.Contains(data, []byte("\r\n")) {
		newline = "\n"
	}
	if newline == "\r\n" {
		block = strings.ReplaceAll(block, "\n", "\r\n")
	}
	updated, err := terminalStartupContent(data, []byte(block), false)
	if err != nil {
		return nil, err
	}
	if order != nil {
		units := utf16.Encode([]rune(string(updated)))
		updated = make([]byte, len(units)*2)
		for i, value := range units {
			order.PutUint16(updated[i*2:], value)
		}
	}
	return append(append([]byte(nil), bom...), updated...), nil
}
