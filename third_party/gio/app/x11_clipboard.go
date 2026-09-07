// SPDX-License-Identifier: Unlicense OR MIT

//go:build ((linux && !android) || freebsd || openbsd) && !nox11

package app

import (
	"io"
	"strings"

	"gioui.org/io/transfer"
)

// x11ClipboardDataEvent completes a clipboard SelectionNotify. X11 replies
// with property None (zero) when no owner exists or conversion is unavailable.
// That is an empty clipboard response, not an event to leave unanswered.
func x11ClipboardDataEvent(selection, clipboard, property, destination uint64, readText func() (string, bool)) (transfer.DataEvent, bool) {
	if selection != clipboard {
		return transfer.DataEvent{}, false
	}
	var text string
	if property != 0 {
		if property != destination {
			return transfer.DataEvent{}, false
		}
		var ok bool
		if text, ok = readText(); !ok {
			return transfer.DataEvent{}, false
		}
	}
	return transfer.DataEvent{
		Type: "application/text",
		Open: func() io.ReadCloser { return io.NopCloser(strings.NewReader(text)) },
	}, true
}
