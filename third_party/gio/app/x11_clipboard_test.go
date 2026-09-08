// SPDX-License-Identifier: Unlicense OR MIT

//go:build ((linux && !android) || freebsd || openbsd) && !nox11

package app

import (
	"io"
	"testing"
)

func TestX11ClipboardSelectionNotify(t *testing.T) {
	const clipboard, destination = 11, 12
	tests := []struct {
		name                string
		selection, property uint64
		text                string
		readOK, wantRead    bool
		wantEvent           bool
	}{
		{name: "no selection owner answers empty", selection: clipboard, property: 0, wantEvent: true},
		{name: "clipboard text", selection: clipboard, property: destination, text: "Kilo Proxy — español 日本語", readOK: true, wantRead: true, wantEvent: true},
		{name: "owned empty clipboard", selection: clipboard, property: destination, readOK: true, wantRead: true, wantEvent: true},
		{name: "other selection with no owner", selection: 99, property: 0},
		{name: "other selection with property", selection: 99, property: destination},
		{name: "other property", selection: clipboard, property: 99},
		{name: "unreadable property", selection: clipboard, property: destination, wantRead: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reads := 0
			data, ok := x11ClipboardDataEvent(tt.selection, clipboard, tt.property, destination, func() (string, bool) {
				reads++
				return tt.text, tt.readOK
			})
			if ok != tt.wantEvent {
				t.Fatalf("delivered clipboard event = %v, want %v", ok, tt.wantEvent)
			}
			wantReads := 0
			if tt.wantRead {
				wantReads = 1
			}
			if reads != wantReads {
				t.Fatalf("read property %d times, want %d", reads, wantReads)
			}
			if !ok {
				return
			}
			if data.Type != "application/text" || data.Open == nil {
				t.Fatalf("invalid clipboard transfer: %#v", data)
			}
			// Each consumer opens an independent stream, including empty replies.
			for i := 0; i < 2; i++ {
				stream := data.Open()
				body, err := io.ReadAll(stream)
				closeErr := stream.Close()
				if err != nil || closeErr != nil || string(body) != tt.text {
					t.Fatalf("clipboard stream = %q, read error %v, close error %v; want %q", body, err, closeErr, tt.text)
				}
			}
		})
	}
}
