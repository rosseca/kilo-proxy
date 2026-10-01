package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestProxyListenErrorClassifiesWrappedSystemErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind proxyListenErrorKind
	}{
		{"occupied", syscall.EADDRINUSE, proxyListenAddressInUse},
		{"access denied", syscall.EACCES, proxyListenPermissionDenied},
		{"operation not permitted", syscall.EPERM, proxyListenPermissionDenied},
		{"address unavailable", syscall.EADDRNOTAVAIL, proxyListenAddressUnavailable},
		{"other system error", syscall.EINVAL, proxyListenUnknown},
		{"untyped occupied text", errors.New("bind: address already in use"), proxyListenUnknown},
		{"untyped permission text", errors.New("bind: permission denied"), proxyListenUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := proxyListenTestNetError(tt.err)
			problem := wrapProxyListenError("127.0.0.1:8877", raw)
			if problem.Kind != tt.kind || problem.Address != "127.0.0.1:8877" || problem.Port != 8877 {
				t.Fatalf("incorrect listener diagnosis: %#v", problem)
			}
			if problem.Unwrap() != raw || !errors.Is(problem, tt.err) {
				t.Fatal("listener diagnosis lost the original error chain")
			}
			var netErr *net.OpError
			var syscallErr *os.SyscallError
			if !errors.As(problem, &netErr) || !errors.As(problem, &syscallErr) {
				t.Fatal("listener diagnosis lost net.OpError or os.SyscallError")
			}
			var errno syscall.Errno
			wantCode := 0
			if errors.As(tt.err, &errno) {
				wantCode = int(errno)
			}
			if problem.SystemCode != wantCode {
				t.Fatalf("system code = %d, want %d", problem.SystemCode, wantCode)
			}
		})
	}
}

func TestProxyListenErrorRealOccupiedPort(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	address := listener.Addr().String()
	other, err := net.Listen("tcp4", address)
	if err == nil {
		_ = other.Close()
		t.Fatal("second listener unexpectedly bound to the occupied address")
	}
	problem := wrapProxyListenError(address, err)
	if problem.Kind != proxyListenAddressInUse || problem.Port != listener.Addr().(*net.TCPAddr).Port {
		t.Fatalf("occupied listener was misdiagnosed: %#v", problem)
	}
	if problem.Err != err || problem.SystemCode == 0 {
		t.Fatal("occupied listener diagnosis lost the OS error or its code")
	}
}

func TestProxyListenErrorMessagesAndDiagnostics(t *testing.T) {
	raw := proxyListenTestNetError(syscall.EACCES)
	problem := wrapProxyListenError("127.0.0.1:18877", raw)
	err := fmt.Errorf("start failed: %w", problem)
	for _, language := range []string{"en", "es", "unsupported"} {
		t.Run(language, func(t *testing.T) {
			message, ok := proxyListenErrorMessage(err, language)
			if !ok || message != problem.Message(language) {
				t.Fatal("message helper did not find the wrapped listener error")
			}
			label := "System error"
			if language == "es" {
				label = "Error del sistema"
			}
			for _, part := range []string{problem.Address, "\n" + label, "(" + proxyListenErrorSystem() + " " + strconv.Itoa(int(syscall.EACCES)) + "): " + raw.Error()} {
				if !strings.Contains(message, part) {
					t.Fatalf("message %q is missing %q", message, part)
				}
			}
			if strings.Contains(strings.SplitN(message, "\n", 2)[0], "already in use") {
				t.Fatal("permission failure incorrectly tells the user that the port is occupied")
			}
		})
	}
	if problem.Error() != problem.Message("en") || problem.Message("unsupported") != problem.Message("en") {
		t.Fatal("default error message must be English")
	}
	details := proxyListenErrorDetails(err)
	if details == nil || details.Kind != proxyListenPermissionDenied || details.Address != problem.Address || details.Port != 18877 || details.SystemCode != int(syscall.EACCES) || details.SystemError != raw.Error() || details.System != proxyListenErrorSystem() {
		t.Fatalf("incorrect diagnostic details: %#v", details)
	}
	data, marshalErr := json.Marshal(details)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["system_error"] != raw.Error() || decoded["system_error_code"] != float64(syscall.EACCES) || decoded["kind"] != "permission_denied" || decoded["address"] != problem.Address || decoded["port"] != float64(18877) {
		t.Fatalf("diagnostic JSON lost error details: %s", data)
	}
}

func TestProxyListenErrorUnknownDoesNotClaimPortConflict(t *testing.T) {
	raw := errors.New("unexpected listener failure")
	problem := wrapProxyListenError("127.0.0.1:8877", raw)
	for _, language := range []string{"en", "es"} {
		message := problem.Message(language)
		for _, unsupportedDiagnosis := range []string{"already in use", "permission", "reserved", "ya está en uso", "reservado", "restringido"} {
			if strings.Contains(message, unsupportedDiagnosis) {
				t.Fatalf("unknown failure made unsupported diagnosis %q: %s", unsupportedDiagnosis, message)
			}
		}
		if !strings.Contains(message, raw.Error()) {
			t.Fatal("unknown failure lost the only available diagnostic detail")
		}
	}
	details := proxyListenErrorDetails(problem)
	data, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "system_error_code") {
		t.Fatalf("diagnostic invented a system error code: %s", data)
	}
}

func TestProxyListenErrorHelpersIgnoreNonListenerErrors(t *testing.T) {
	var typedNil *proxyListenError
	for _, err := range []error{nil, errors.New("login pending"), typedNil} {
		if message, ok := proxyListenErrorMessage(err, "en"); ok || message != "" {
			t.Fatalf("message helper claimed a non-listener error: %q", message)
		}
		if details := proxyListenErrorDetails(err); details != nil {
			t.Fatalf("diagnostic helper claimed a non-listener error: %#v", details)
		}
	}
	if wrapProxyListenError("127.0.0.1:8877", nil) != nil {
		t.Fatal("nil listener error must stay nil")
	}
}

func TestProxyListenErrorPreservesAddress(t *testing.T) {
	for _, tt := range []struct {
		address string
		port    int
	}{
		{"127.0.0.1:8877", 8877},
		{"[::1]:18877", 18877},
		{"invalid-address", 0},
	} {
		problem := wrapProxyListenError(tt.address, syscall.EINVAL)
		if problem.Address != tt.address || problem.Port != tt.port {
			t.Fatalf("address %q was changed in diagnosis: %#v", tt.address, problem)
		}
	}
}

func proxyListenTestNetError(err error) error {
	return fmt.Errorf("listener failed: %w", &net.OpError{
		Op: "listen", Net: "tcp4", Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8877},
		Err: &os.SyscallError{Syscall: "bind", Err: err},
	})
}
