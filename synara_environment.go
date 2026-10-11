package main

import (
	"errors"
	"io"
	"sort"
	"strings"
)

// Synara Beta rehydrates undefined Windows environment variables from the
// registry. Empty sentinels are not safe: Electron's Node switch and the vendor
// source marker are presence-sensitive, and its backend validates URL values.
// Inspect names only, without reading stored values or changing the registry.
func synaraPersistedOverrides(names []string) []string {
	reset := map[string]bool{}
	for _, name := range synaraUnsetEnvironment(nil) {
		reset[strings.ToUpper(name)] = true
	}
	reset["VITE_DEV_SERVER_URL"] = true
	result := []string{}
	seen := map[string]bool{}
	for _, name := range names {
		canonical := strings.ToUpper(name)
		switch canonical {
		case "SYNARA_HOME", "SYNARA_BETA_HOME", "SYNARA_DESKTOP_SMOKE_USER_DATA", "SYNARA_DISABLE_AUTO_UPDATE":
			continue // Defined child overrides cannot be rehydrated.
		}
		if !seen[canonical] && (strings.HasPrefix(canonical, "SYNARA_") || reset[canonical]) {
			seen[canonical] = true
			result = append(result, canonical)
		}
	}
	sort.Strings(result)
	return result
}

// A bounded registry enumeration ends with io.EOF when the key contains fewer
// names than requested. Other errors or reaching the limit are incomplete.
func synaraRegistryNamesComplete(names []string, err error) bool {
	return len(names) < 512 && (err == nil || errors.Is(err, io.EOF))
}
