//go:build windows

package main

import (
	"errors"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const claudeDesktopWindowsPolicyPath = `SOFTWARE\Policies\Claude`

// This interface deliberately exposes only value names and handle cleanup.
// Managed policy values may contain credentials and must never be read here.
type claudeDesktopRegistryNamesKey interface {
	ReadValueNames(int) ([]string, error)
	Close() error
}

type claudeDesktopRegistryNamesOpener func(registry.Key, string, uint32) (claudeDesktopRegistryNamesKey, error)

func claudeDesktopWindowsManagedKeys() ([]string, error) {
	return claudeDesktopWindowsManagedKeysWith(func(hive registry.Key, path string, access uint32) (claudeDesktopRegistryNamesKey, error) {
		return registry.OpenKey(hive, path, access)
	})
}

func claudeDesktopWindowsManagedKeysWith(open claudeDesktopRegistryNamesOpener) ([]string, error) {
	for _, hive := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		// QUERY_VALUE grants only the access needed to enumerate value names.
		// Native registry calls do not depend on PowerShell's parser, language,
		// availability, or permission to launch a helper process.
		key, err := open(hive, claudeDesktopWindowsPolicyPath, registry.QUERY_VALUE)
		if err != nil {
			if errors.Is(err, registry.ErrNotExist) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
				continue
			}
			return nil, err
		}
		names, err := key.ReadValueNames(-1)
		closeErr := key.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		// Desktop falls back to HKCU when HKLM has no policy values. A
		// populated HKLM policy key takes precedence over HKCU.
		if len(names) == 0 {
			continue
		}
		return names, nil
	}
	return nil, nil
}
