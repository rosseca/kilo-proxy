package main

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

func classifyProxyListenError(err error) proxyListenErrorKind {
	// Winsock uses its own numeric error codes. Go's portable EADDRINUSE and
	// EACCES constants are not aliases of the errors returned by Winsock.
	switch {
	case errors.Is(err, windows.WSAEADDRINUSE), errors.Is(err, syscall.EADDRINUSE):
		return proxyListenAddressInUse
	case errors.Is(err, windows.WSAEACCES), errors.Is(err, windows.ERROR_ACCESS_DENIED),
		errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return proxyListenPermissionDenied
	case errors.Is(err, windows.WSAEADDRNOTAVAIL), errors.Is(err, syscall.EADDRNOTAVAIL):
		return proxyListenAddressUnavailable
	default:
		return proxyListenUnknown
	}
}
