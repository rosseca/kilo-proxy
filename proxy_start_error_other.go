//go:build !windows

package main

import (
	"errors"
	"syscall"
)

func classifyProxyListenError(err error) proxyListenErrorKind {
	switch {
	case errors.Is(err, syscall.EADDRINUSE):
		return proxyListenAddressInUse
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return proxyListenPermissionDenied
	case errors.Is(err, syscall.EADDRNOTAVAIL):
		return proxyListenAddressUnavailable
	default:
		return proxyListenUnknown
	}
}
