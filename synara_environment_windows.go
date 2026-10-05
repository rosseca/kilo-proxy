package main

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

func synaraWindowsEnvironmentSafe() error {
	names := []string{}
	for _, scope := range []struct {
		root registry.Key
		path string
	}{
		{registry.CURRENT_USER, `Environment`},
		{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`},
	} {
		key, err := registry.OpenKey(scope.root, scope.path, registry.QUERY_VALUE)
		if errors.Is(err, registry.ErrNotExist) {
			continue
		}
		if err != nil {
			return errors.New("Cannot inspect global Windows environment overrides. Synara was not opened.")
		}
		registered, err := key.ReadValueNames(512)
		_ = key.Close()
		if !synaraRegistryNamesComplete(registered, err) {
			return errors.New("Cannot inspect global Windows environment overrides. Synara was not opened.")
		}
		names = append(names, registered...)
	}
	if len(synaraPersistedOverrides(names)) > 0 {
		return errors.New("Remove globally saved Synara, provider or development environment overrides from Windows Environment Variables before opening Synara Kilo. Kilo Proxy does not change the registry.")
	}
	return nil
}
