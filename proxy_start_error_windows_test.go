package main

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestProxyListenErrorWindowsWinsockCodes(t *testing.T) {
	tests := []struct {
		name string
		err  syscall.Errno
		code int
		kind proxyListenErrorKind
	}{
		{"Winsock occupied", windows.WSAEADDRINUSE, 10048, proxyListenAddressInUse},
		{"Winsock denied", windows.WSAEACCES, 10013, proxyListenPermissionDenied},
		{"Winsock address unavailable", windows.WSAEADDRNOTAVAIL, 10049, proxyListenAddressUnavailable},
		{"Windows access denied", windows.ERROR_ACCESS_DENIED, 5, proxyListenPermissionDenied},
		{"other Winsock error", windows.WSAEINVAL, 10022, proxyListenUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate OS text in another language; only the wrapped code is
			// reliable for classification across Windows language settings.
			localized := &proxyListenTestLocalizedWindowsError{Err: tt.err}
			raw := proxyListenTestNetError(localized)
			problem := wrapProxyListenError("127.0.0.1:8877", raw)
			if problem.Kind != tt.kind || problem.SystemCode != tt.code {
				t.Fatalf("Winsock error misdiagnosed: %#v", problem)
			}
			if !errors.Is(problem, tt.err) {
				t.Fatal("Windows diagnosis lost the original Winsock error")
			}
			if !strings.Contains(problem.Message("en"), fmt.Sprintf("System error (Windows %d): %s", tt.code, raw.Error())) {
				t.Fatalf("Windows diagnosis lost the numeric code or original text: %s", problem)
			}
			if tt.kind == proxyListenPermissionDenied {
				if !strings.Contains(problem.Message("en"), "Windows denied binding") || !strings.Contains(problem.Message("en"), "may be reserved or restricted") {
					t.Fatalf("Windows denied message lacks the conditional reservation explanation: %s", problem)
				}
				if !strings.Contains(problem.Message("es"), "puede estar reservado o restringido") {
					t.Fatalf("Spanish denied message lacks the conditional reservation explanation: %s", problem.Message("es"))
				}
			}
		})
	}
}

type proxyListenTestLocalizedWindowsError struct{ Err error }

func (e *proxyListenTestLocalizedWindowsError) Error() string {
	return "mensaje del sistema localizado"
}
func (e *proxyListenTestLocalizedWindowsError) Unwrap() error { return e.Err }
