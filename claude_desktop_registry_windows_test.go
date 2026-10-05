//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type claudeDesktopRegistryTestKey struct {
	names    []string
	readErr  error
	closeErr error
	reads    int
	closes   int
}

func (k *claudeDesktopRegistryTestKey) ReadValueNames(n int) ([]string, error) {
	if n != -1 {
		return nil, fmt.Errorf("expected all policy value names")
	}
	k.reads++
	return k.names, k.readErr
}

func (k *claudeDesktopRegistryTestKey) Close() error {
	k.closes++
	return k.closeErr
}

func TestClaudeDesktopWindowsManagedKeys(t *testing.T) {
	tests := []struct {
		name        string
		machine     *claudeDesktopRegistryTestKey
		machineErr  error
		user        *claudeDesktopRegistryTestKey
		userErr     error
		want        []string
		wantErr     error
		wantHives   []registry.Key
		wantManaged bool
	}{
		{
			name:       "no policies",
			machineErr: registry.ErrNotExist, userErr: registry.ErrNotExist,
			wantHives: []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER},
		},
		{
			name:       "missing policy path",
			machineErr: windows.ERROR_PATH_NOT_FOUND, userErr: windows.ERROR_PATH_NOT_FOUND,
			wantHives: []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER},
		},
		{
			name:    "machine app policy takes precedence",
			machine: &claudeDesktopRegistryTestKey{names: []string{"disableAutoUpdates"}},
			user:    &claudeDesktopRegistryTestKey{names: []string{"inferenceProvider"}},
			want:    []string{"disableAutoUpdates"}, wantHives: []registry.Key{registry.LOCAL_MACHINE},
		},
		{
			name:      "empty machine key falls back to user inference policy",
			machine:   &claudeDesktopRegistryTestKey{},
			user:      &claudeDesktopRegistryTestKey{names: []string{"inferenceGatewayApiKey"}},
			want:      []string{"inferenceGatewayApiKey"},
			wantHives: []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER}, wantManaged: true,
		},
		{
			name:      "both policy keys empty",
			machine:   &claudeDesktopRegistryTestKey{},
			user:      &claudeDesktopRegistryTestKey{},
			wantHives: []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER},
		},
		{
			name:       "user inference policy",
			machineErr: fmt.Errorf("open: %w", registry.ErrNotExist),
			user:       &claudeDesktopRegistryTestKey{names: []string{"inferenceProvider", "deploymentDisplayName"}},
			want:       []string{"inferenceProvider", "deploymentDisplayName"},
			wantHives:  []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER}, wantManaged: true,
		},
		{
			name:       "machine access denied fails closed",
			machineErr: windows.ERROR_ACCESS_DENIED,
			user:       &claudeDesktopRegistryTestKey{},
			wantErr:    windows.ERROR_ACCESS_DENIED, wantHives: []registry.Key{registry.LOCAL_MACHINE},
		},
		{
			name:       "user access denied fails closed",
			machineErr: registry.ErrNotExist, userErr: windows.ERROR_ACCESS_DENIED,
			wantErr:   windows.ERROR_ACCESS_DENIED,
			wantHives: []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER},
		},
		{
			name:    "enumeration failure discards partial names and closes key",
			machine: &claudeDesktopRegistryTestKey{names: []string{"disableAutoUpdates"}, readErr: windows.ERROR_ACCESS_DENIED},
			wantErr: windows.ERROR_ACCESS_DENIED, wantHives: []registry.Key{registry.LOCAL_MACHINE},
		},
		{
			name:    "enumeration not found is not an absent key",
			machine: &claudeDesktopRegistryTestKey{readErr: registry.ErrNotExist},
			wantErr: registry.ErrNotExist, wantHives: []registry.Key{registry.LOCAL_MACHINE},
		},
		{
			name:    "close failure fails closed",
			machine: &claudeDesktopRegistryTestKey{closeErr: windows.ERROR_INVALID_HANDLE},
			wantErr: windows.ERROR_INVALID_HANDLE, wantHives: []registry.Key{registry.LOCAL_MACHINE},
		},
		{
			name:    "names remain intact without PowerShell line parsing",
			machine: &claudeDesktopRegistryTestKey{names: []string{"inference\nProvider", ""}},
			want:    []string{"inference\nProvider", ""}, wantHives: []registry.Key{registry.LOCAL_MACHINE}, wantManaged: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hives []registry.Key
			opener := func(hive registry.Key, path string, access uint32) (claudeDesktopRegistryNamesKey, error) {
				hives = append(hives, hive)
				if path != claudeDesktopWindowsPolicyPath || access != registry.QUERY_VALUE {
					t.Fatal("policy inspection must request only the Claude policy value names")
				}
				if hive == registry.LOCAL_MACHINE {
					return tt.machine, tt.machineErr
				}
				return tt.user, tt.userErr
			}
			got, err := claudeDesktopWindowsManagedKeysWith(opener)
			if !errors.Is(err, tt.wantErr) || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("unexpected names/error: got %q, %v; want %q, %v", got, err, tt.want, tt.wantErr)
			}
			if !reflect.DeepEqual(hives, tt.wantHives) {
				t.Fatalf("unexpected hive precedence: got %v; want %v", hives, tt.wantHives)
			}
			for _, key := range []*claudeDesktopRegistryTestKey{tt.machine, tt.user} {
				if key != nil && (key.reads != key.closes || key.reads > 1) {
					t.Fatalf("policy handles must be enumerated once and closed: reads=%d closes=%d", key.reads, key.closes)
				}
			}
			if err == nil && claudeDesktopManagedKeys(got) != tt.wantManaged {
				t.Fatal("unexpected managed-inference classification")
			}
		})
	}
}

// Opt-in acceptance coverage reads only the installed policy value names. It
// never reads their data, creates or changes registry keys, or prints names.
func TestClaudeDesktopWindowsInstalledManagedKeys(t *testing.T) {
	if os.Getenv("KILO_TEST_WINDOWS_DESKTOP") != "1" {
		t.Skip("set KILO_TEST_WINDOWS_DESKTOP=1 to inspect installed Windows policy names read-only")
	}
	if _, err := claudeDesktopWindowsManagedKeys(); err != nil {
		t.Fatal("cannot inspect installed Windows Claude Desktop policy names:", err)
	}
}
