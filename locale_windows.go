package main

import (
	"os"
	"syscall"
	"unsafe"
)

func systemLocalePreferences() []string {
	// Read the user's UI language order directly, without starting PowerShell.
	getPreferred := syscall.NewLazyDLL("kernel32.dll").NewProc("GetUserPreferredUILanguages")
	const muiLanguageName = 0x8
	var count, size uint32
	if ok, _, _ := getPreferred.Call(muiLanguageName, uintptr(unsafe.Pointer(&count)), 0, uintptr(unsafe.Pointer(&size))); ok == 0 || size == 0 || size > 65536 {
		return environmentLocalePreferences(os.Getenv)
	}
	buffer := make([]uint16, size)
	if ok, _, _ := getPreferred.Call(muiLanguageName, uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size))); ok == 0 {
		return environmentLocalePreferences(os.Getenv)
	}
	var locales []string
	start := 0
	for i, c := range buffer {
		if c != 0 {
			continue
		}
		if i == start {
			break
		}
		locales = append(locales, syscall.UTF16ToString(buffer[start:i]))
		start = i + 1
	}
	if len(locales) == 0 {
		return environmentLocalePreferences(os.Getenv)
	}
	return locales
}
