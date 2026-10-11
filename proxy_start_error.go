package main

import (
	"errors"
	"fmt"
	"net"
	"runtime"
	"strconv"
	"syscall"
)

type proxyListenErrorKind string

const (
	proxyListenAddressInUse       proxyListenErrorKind = "address_in_use"
	proxyListenPermissionDenied   proxyListenErrorKind = "permission_denied"
	proxyListenAddressUnavailable proxyListenErrorKind = "address_unavailable"
	proxyListenUnknown            proxyListenErrorKind = "unknown"
)

// proxyListenError is only for errors returned by the local proxy listener.
// Keeping the original error allows callers to inspect the complete net and OS
// error chain without depending on the language of the system error text.
type proxyListenError struct {
	Address    string
	Port       int
	Kind       proxyListenErrorKind
	Err        error
	SystemCode int
}

func wrapProxyListenError(address string, err error) *proxyListenError {
	if err == nil {
		return nil
	}
	problem := &proxyListenError{Address: address, Kind: classifyProxyListenError(err), Err: err}
	if _, port, splitErr := net.SplitHostPort(address); splitErr == nil {
		problem.Port, _ = strconv.Atoi(port)
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		problem.SystemCode = int(errno)
	}
	return problem
}

func (e *proxyListenError) Error() string { return e.Message("en") }

func (e *proxyListenError) Unwrap() error { return e.Err }

func (e *proxyListenError) Message(language string) string {
	var message string
	if language == "es" {
		switch e.Kind {
		case proxyListenAddressInUse:
			message = fmt.Sprintf("No se pudo iniciar Kilo Proxy en %s: el puerto ya está en uso. Si hay otra instancia de Kilo Proxy en ejecución, ciérrala desde la bandeja del sistema; si no, elige otro puerto local.", e.Address)
		case proxyListenPermissionDenied:
			if runtime.GOOS == "windows" {
				message = fmt.Sprintf("Windows no permitió que Kilo Proxy escuchara en %s. El puerto puede estar reservado o restringido. Elige otro puerto local o revisa las restricciones de red del sistema.", e.Address)
			} else {
				message = fmt.Sprintf("El sistema no permitió que Kilo Proxy escuchara en %s. Revisa los permisos de red locales o elige otro puerto local.", e.Address)
			}
		case proxyListenAddressUnavailable:
			message = fmt.Sprintf("No se pudo iniciar Kilo Proxy en %s: la dirección local no está disponible. Comprueba que la interfaz de red local (loopback) esté disponible y vuelve a intentarlo.", e.Address)
		default:
			message = fmt.Sprintf("No se pudo iniciar Kilo Proxy en %s. El sistema no pudo abrir el puerto local. Consulta el error del sistema que aparece debajo para obtener más detalles.", e.Address)
		}
	} else {
		switch e.Kind {
		case proxyListenAddressInUse:
			message = fmt.Sprintf("Cannot start Kilo Proxy on %s: the port is already in use. If another Kilo Proxy instance is running, quit it from the system tray; otherwise choose another local port.", e.Address)
		case proxyListenPermissionDenied:
			if runtime.GOOS == "windows" {
				message = fmt.Sprintf("Windows denied binding Kilo Proxy to %s. The port may be reserved or restricted. Choose another local port or check your system's network restrictions.", e.Address)
			} else {
				message = fmt.Sprintf("The system denied binding Kilo Proxy to %s. Check the local network permissions or choose another local port.", e.Address)
			}
		case proxyListenAddressUnavailable:
			message = fmt.Sprintf("Cannot start Kilo Proxy on %s: the local address is not available. Check that the local loopback network interface is available and try again.", e.Address)
		default:
			message = fmt.Sprintf("Cannot start Kilo Proxy on %s. The system could not open the local listener. Check the system error below for more details.", e.Address)
		}
	}

	system := proxyListenErrorSystem()
	if e.SystemCode != 0 {
		system += " " + strconv.Itoa(e.SystemCode)
	}
	if language == "es" {
		return message + fmt.Sprintf("\nError del sistema (%s): %s", system, e.Err)
	}
	return message + fmt.Sprintf("\nSystem error (%s): %s", system, e.Err)
}

func proxyListenErrorSystem() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	default:
		return runtime.GOOS
	}
}

func proxyListenErrorMessage(err error, language string) (string, bool) {
	var problem *proxyListenError
	if !errors.As(err, &problem) || problem == nil {
		return "", false
	}
	return problem.Message(language), true
}

type proxyListenErrorDiagnostic struct {
	Kind        proxyListenErrorKind `json:"kind"`
	Address     string               `json:"address"`
	Port        int                  `json:"port"`
	System      string               `json:"system"`
	SystemCode  int                  `json:"system_error_code,omitempty"`
	SystemError string               `json:"system_error"`
}

func proxyListenErrorDetails(err error) *proxyListenErrorDiagnostic {
	var problem *proxyListenError
	if !errors.As(err, &problem) || problem == nil {
		return nil
	}
	return &proxyListenErrorDiagnostic{
		Kind: problem.Kind, Address: problem.Address, Port: problem.Port,
		System: proxyListenErrorSystem(), SystemCode: problem.SystemCode, SystemError: problem.Err.Error(),
	}
}
