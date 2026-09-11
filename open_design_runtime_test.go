package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOpenDesignManagedNamespaceAndPathsAreInstallationScoped(t *testing.T) {
	root := t.TempDir()
	paths := openDesignProfilePaths(root)
	if !openDesignNamespacePattern.MatchString(paths.Namespace) || paths.Namespace != openDesignNamespace(filepath.Join(root, "unused", "..")) {
		t.Fatal("namespace is not stable after path cleaning")
	}
	if paths.Namespace == openDesignNamespace(filepath.Join(root, "another-installation")) {
		t.Fatal("distinct Kilo installations share Open Design ownership")
	}
	if paths.Root != filepath.Join(root, "open-design") || paths.NamespaceBase != filepath.Join(paths.Root, "namespaces") || paths.Config != filepath.Join(paths.NamespaceBase, paths.Namespace, "data", "app-config.json") || paths.Runtime != filepath.Join(paths.NamespaceBase, paths.Namespace, "runtime") || paths.Profiles != filepath.Join(paths.Root, "profiles") {
		t.Fatal("managed paths do not follow the packaged namespace contract")
	}
	if _, err := os.Stat(paths.Root); !os.IsNotExist(err) {
		t.Fatal("path discovery wrote Open Design state")
	}
}

func TestConfigureOpenDesignLaunchIsolatesWithoutWritingState(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "Open Design.app")
	binary := filepath.Join(bundle, "Contents", "MacOS", "Open Design")
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("synthetic fixture; never executed"), 0700); err != nil {
		t.Fatal(err)
	}
	writeOpenDesignVersionFixture(t, bundle, "macos", `{"appVersion":"0.22.2"}`)
	plan := clientLaunchPlan{Executable: bundle, Args: []string{"old-project"}, Env: map[string]string{"KILO_LOCAL_API_KEY": "synthetic-local", "OD_DATA_DIR": "/normal/profile", "OD_SIDECAR_SUPERVISED_CONTEXT": "old-owner"}}
	if err := configureOpenDesignLaunch(&plan, root, "macos"); err != nil {
		t.Fatal(err)
	}
	paths := openDesignProfilePaths(root)
	if plan.Executable != binary || len(plan.Args) != 0 || plan.Env["OD_PACKAGED_NAMESPACE"] != paths.Namespace || plan.Env["OD_PACKAGED_NAMESPACE_BASE_ROOT"] != paths.NamespaceBase || plan.Env["OD_UPDATE_ENABLED"] != "0" {
		t.Fatal("launch does not isolate the packaged desktop")
	}
	if plan.Env["KILO_LOCAL_API_KEY"] != "synthetic-local" || plan.Env["OD_DATA_DIR"] != "" || plan.Env["OD_SIDECAR_SUPERVISED_CONTEXT"] != "" {
		t.Fatal("launch changed caller credentials or retained conflicting profile settings")
	}
	for _, key := range []string{"OD_DATA_DIR", "OD_PACKAGED_CONFIG_PATH", "OD_LEGACY_DATA_DIR", "OD_SIDECAR_SUPERVISED_CONTEXT", "OD_SIDECAR_SUPERVISOR_TARGET", "ELECTRON_RUN_AS_NODE"} {
		if !containsOpenDesignString(plan.Unset, key) {
			t.Fatalf("launch retained inherited %s", key)
		}
	}
	if _, err := os.Stat(paths.Root); !os.IsNotExist(err) {
		t.Fatal("launch environment preparation wrote a profile")
	}
	if err := configureOpenDesignLaunch(&clientLaunchPlan{Executable: filepath.Join(root, "missing.app")}, root, "macos"); err == nil {
		t.Fatal("missing desktop executable accepted")
	}
	if err := configureOpenDesignLaunch(&clientLaunchPlan{}, root, "linux"); err == nil {
		t.Fatal("source-only Linux accepted as a supported desktop")
	}
	windowsPlan := clientLaunchPlan{Executable: filepath.Join(root, "Open Design.exe"), Args: []string{"project"}}
	writeOpenDesignVersionFixture(t, windowsPlan.Executable, "windows", `{"appVersion":"0.22.2"}`)
	if err := configureOpenDesignLaunch(&windowsPlan, root, "windows"); err != nil || len(windowsPlan.Args) != 0 || windowsPlan.Executable != filepath.Join(root, "Open Design.exe") {
		t.Fatal("Windows launch gained a shell wrapper or application flags")
	}
}

func TestOpenDesignRunningGateUsesLiveOwnershipAndFailsClosed(t *testing.T) {
	namespace := "kilo-proxy-0123456789ab"
	stamp := "electron supervisor.mjs --od-stamp-channel=stable --od-stamp-namespace=" + namespace + " --od-stamp-source=packaged --od-stamp-mode=runtime --od-stamp-app=desktop"
	for _, tc := range []struct {
		name, command       string
		snapshotErr, ipcErr bool
		ipcRunning, want    bool
		wantErr             bool
	}{
		{name: "supervisor", command: stamp, want: true},
		{name: "starting launcher", command: stamp + " --od-sidecar-lifecycle=launcher", want: true},
		{name: "partial startup", command: "electron --od-stamp-namespace=" + namespace, want: true},
		{name: "different namespace", command: strings.ReplaceAll(stamp, namespace, namespace+"1")},
		{name: "inexact argument", command: "electron --od-stamp-namespace=" + namespace + "-other"},
		{name: "socket during handoff", ipcRunning: true, want: true},
		{name: "socket with failed snapshot", snapshotErr: true, ipcRunning: true, want: true},
		{name: "snapshot failure", snapshotErr: true, wantErr: true},
		{name: "IPC uncertainty", ipcErr: true, wantErr: true},
		{name: "closed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			running, err := openDesignRunningWith(namespace, func(context.Context) ([]string, error) {
				if tc.snapshotErr {
					return nil, errors.New("synthetic-secret that must not be returned")
				}
				return []string{tc.command}, nil
			}, func(context.Context, string) (bool, error) {
				if tc.ipcErr {
					return false, errors.New("synthetic IPC secret")
				}
				return tc.ipcRunning, nil
			})
			if running != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("running=%v error=%v", running, err)
			}
			if err != nil && err != errOpenDesignRunningCheck {
				t.Fatal("process lookup exposed its internal failure")
			}
		})
	}
	called := false
	_, err := openDesignRunningWith("unmanaged", func(context.Context) ([]string, error) { called = true; return nil, nil }, func(context.Context, string) (bool, error) { called = true; return false, nil })
	if err == nil || called {
		t.Fatal("unmanaged namespace triggered a process probe")
	}
}

func TestOpenDesignRuntimeProbeIdentitiesAreExact(t *testing.T) {
	stamps := openDesignIPCStamps("kilo-proxy-0123456789ab")
	seen := map[string]bool{}
	for _, stamp := range stamps {
		digest := openDesignIPCDigest("501", stamp)
		if len(digest) != 32 || seen[digest] {
			t.Fatal("IPC identities collided or have an invalid digest")
		}
		seen[digest] = true
	}
	if len(seen) != 12 || !reflect.DeepEqual(stamps[0], openDesignIPCStamp{"stable", "kilo-proxy-0123456789ab", "packaged", "runtime", "desktop"}) {
		t.Fatal("probe does not include exact packaged supervisor identity")
	}
	if openDesignIPCDigest("501", stamps[0]) != "d2759788ac4108d171a4ffb4b1cb481a" {
		t.Fatal("IPC digest no longer matches the released field order and newline format")
	}
	if openDesignIPCDigest("501", stamps[0]) == openDesignIPCDigest("502", stamps[0]) {
		t.Fatal("IPC identities crossed OS users")
	}
}

func TestOpenDesignProcessSnapshotOutputIsBounded(t *testing.T) {
	output := &openDesignLimitedOutput{limit: 4}
	if _, err := output.Write([]byte("four")); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("private")); err == nil || !output.exceeded || output.String() != "four" {
		t.Fatal("oversized process output was retained or accepted as complete")
	}
}
