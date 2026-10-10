package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func headlessUpdateTestDependencies(t *testing.T, dir string) (headlessUpdateDependencies, *int) {
	t.Helper()
	upgrades := new(int)
	rt := packageUpdateRuntime{platform: "linux", home: t.TempDir(), binary: "/usr/bin/kilo-proxy-headless", current: "0.57.0", uid: 1000}
	in := packageInstallation{Method: "apt", Manager: "/usr/bin/apt-get", Package: "kilo-proxy-headless", Binary: rt.binary, Relaunch: rt.binary, Version: rt.current, Digest: "original"}
	deps := headlessUpdateDependencies{
		runtime: func() (packageUpdateRuntime, error) { return rt, nil },
		detect:  func(packageUpdateRuntime) (packageInstallation, error) { return in, nil },
		latest:  func(context.Context) (string, error) { return "0.58.0", nil },
		upgrade: func(_ context.Context, received packageInstallation, target string, _ []string, _, _ io.Writer) (string, error) {
			*upgrades++
			if received != in || target != "0.58.0" {
				t.Fatal("wrong update target or package")
			}
			if unlock, err := acquireProfileLock(dir); !errors.Is(err, errProfileLocked) {
				if unlock != nil {
					unlock()
				}
				t.Fatal("update must hold profile lock", err)
			}
			return "/usr/bin/kilo-proxy-headless", nil
		},
		verify: func(received packageUpdateRuntime, previous packageInstallation, binary, target string) error {
			if received.binary != rt.binary || previous != in || binary != in.Relaunch || target != "0.58.0" {
				t.Fatal("wrong post-update verification scope")
			}
			return nil
		},
		wrappers: func(home, profile, oldBinary, newBinary string) error {
			if home != rt.home || profile != dir || oldBinary != rt.binary || newBinary != in.Relaunch {
				t.Fatal("wrong wrapper refresh scope")
			}
			return nil
		},
	}
	return deps, upgrades
}

func TestHeadlessPackageUpdateCheckDoesNotMutateProfile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "unused-profile")
	deps, upgrades := headlessUpdateTestDependencies(t, dir)
	var output bytes.Buffer
	if code := runHeadlessUpdateWith(dir, nil, strings.NewReader(""), &output, &output, deps); code != 0 {
		t.Fatal(code, output.String())
	}
	if *upgrades != 0 || !strings.Contains(output.String(), "update install") {
		t.Fatal(output.String())
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("check created or opened profile", err)
	}
}

func TestHeadlessPackageUpdateRequiresExplicitConfirmation(t *testing.T) {
	for _, answer := range []string{"", "n\n", "no\n", "anything\n"} {
		t.Run(answer, func(t *testing.T) {
			dir := t.TempDir()
			deps, upgrades := headlessUpdateTestDependencies(t, dir)
			var output bytes.Buffer
			if code := runHeadlessUpdateWith(dir, []string{"install"}, strings.NewReader(answer), &output, &output, deps); code != 0 {
				t.Fatal(code, output.String())
			}
			if *upgrades != 0 || !strings.Contains(output.String(), "cancelled") {
				t.Fatal(output.String())
			}
		})
	}
	for _, args := range [][]string{{"install"}, {"install", "--yes"}} {
		dir := t.TempDir()
		deps, upgrades := headlessUpdateTestDependencies(t, dir)
		var output bytes.Buffer
		if code := runHeadlessUpdateWith(dir, args, strings.NewReader("yes\n"), &output, &output, deps); code != 0 {
			t.Fatal(code, output.String())
		}
		if *upgrades != 1 || !strings.Contains(output.String(), "Start serve again") {
			t.Fatal(output.String())
		}
		unlock, err := acquireProfileLock(dir)
		if err != nil {
			t.Fatal("lock was not released", err)
		}
		unlock()
	}
}

func TestHeadlessPackageUpdateRefusesBusyProfileAndInstalledService(t *testing.T) {
	for _, state := range []string{"busy", "service", "symlink"} {
		t.Run(state, func(t *testing.T) {
			dir := t.TempDir()
			deps, upgrades := headlessUpdateTestDependencies(t, dir)
			switch state {
			case "busy":
				unlock, err := acquireProfileLock(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer unlock()
			case "service":
				if err := os.WriteFile(filepath.Join(dir, headlessServiceManifestFile), []byte("untrusted"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if runtime.GOOS == "windows" {
					t.Skip("Headless installations are supported on macOS and Linux.")
				}
				if err := os.Symlink(filepath.Join(dir, "absent-target"), filepath.Join(dir, headlessServiceManifestFile)); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			if code := runHeadlessUpdateWith(dir, []string{"install", "--yes"}, strings.NewReader(""), &output, &output, deps); code != 1 || *upgrades != 0 {
				t.Fatal(code, output.String())
			}
		})
	}
}

type headlessUpdateConfirmReader struct {
	before func()
	input  io.Reader
}

func (r *headlessUpdateConfirmReader) Read(data []byte) (int, error) {
	if r.before != nil {
		r.before()
		r.before = nil
	}
	return r.input.Read(data)
}

func TestHeadlessPackageUpdateRechecksServiceAfterConfirmation(t *testing.T) {
	dir := t.TempDir()
	deps, upgrades := headlessUpdateTestDependencies(t, dir)
	input := &headlessUpdateConfirmReader{input: strings.NewReader("yes\n"), before: func() {
		if err := os.WriteFile(filepath.Join(dir, headlessServiceManifestFile), []byte("new service"), 0600); err != nil {
			t.Fatal(err)
		}
	}}
	var output bytes.Buffer
	if code := runHeadlessUpdateWith(dir, []string{"install"}, input, &output, &output, deps); code != 1 || *upgrades != 0 || !strings.Contains(output.String(), "service uninstall") {
		t.Fatal(code, output.String())
	}
}

func TestHeadlessPackageUpdateRejectsChangedInstallationAndInvalidRelease(t *testing.T) {
	for _, state := range []string{"changed", "prerelease", "invalid", "latest-error", "upgrade-error", "verify-error", "wrappers-error"} {
		t.Run(state, func(t *testing.T) {
			dir := t.TempDir()
			deps, upgrades := headlessUpdateTestDependencies(t, dir)
			switch state {
			case "changed":
				original := deps.detect
				calls := 0
				deps.detect = func(rt packageUpdateRuntime) (packageInstallation, error) {
					in, err := original(rt)
					calls++
					if calls > 1 {
						in.Digest = "changed"
					}
					return in, err
				}
			case "prerelease":
				deps.latest = func(context.Context) (string, error) { return "0.58.0-beta.1", nil }
			case "invalid":
				deps.latest = func(context.Context) (string, error) { return "garbage", nil }
			case "latest-error":
				deps.latest = func(context.Context) (string, error) { return "", errors.New("offline") }
			case "upgrade-error":
				deps.upgrade = func(context.Context, packageInstallation, string, []string, io.Writer, io.Writer) (string, error) {
					return "", errors.New("feed not synchronized")
				}
			case "wrappers-error":
				deps.wrappers = func(string, string, string, string) error { return errors.New("wrapper changed") }
			case "verify-error":
				deps.verify = func(packageUpdateRuntime, packageInstallation, string, string) error {
					return errors.New("ownership changed")
				}
			}
			var output bytes.Buffer
			if code := runHeadlessUpdateWith(dir, []string{"install", "--yes"}, strings.NewReader(""), &output, &output, deps); code != 1 || strings.Contains(output.String(), "Update complete") {
				t.Fatal(code, output.String())
			}
			if state != "wrappers-error" && state != "verify-error" && *upgrades != 0 {
				t.Fatal("unexpected package manager execution")
			}
		})
	}
}

func TestHeadlessPackageUpdateManualAndCurrent(t *testing.T) {
	for _, state := range []string{"manual", "current", "newer"} {
		t.Run(state, func(t *testing.T) {
			dir := t.TempDir()
			deps, upgrades := headlessUpdateTestDependencies(t, dir)
			expectCode := 0
			if state == "manual" {
				deps.detect = func(rt packageUpdateRuntime) (packageInstallation, error) {
					return packageInstallation{Method: "manual", Binary: rt.binary}, nil
				}
				expectCode = 1
			} else {
				latest := "0.57.0"
				if state == "newer" {
					latest = "0.56.3"
				}
				deps.latest = func(context.Context) (string, error) { return latest, nil }
			}
			var output bytes.Buffer
			if code := runHeadlessUpdateWith(dir, []string{"install", "--yes"}, strings.NewReader(""), &output, &output, deps); code != expectCode || *upgrades != 0 {
				t.Fatal(code, output.String())
			}
		})
	}
}

func TestHeadlessPackageUpdateCommandParsing(t *testing.T) {
	command, dir, args, handled, err := parseHeadlessCommand([]string{"--config-dir", "/tmp/profile", "update", "install", "--yes"})
	if err != nil || !handled || command != "update" || dir != "/tmp/profile" || strings.Join(args, " ") != "install --yes" {
		t.Fatal(command, dir, args, handled, err)
	}
	deps, upgrades := headlessUpdateTestDependencies(t, t.TempDir())
	for _, args := range [][]string{{"check", "--yes"}, {"install", "--yes", "extra"}, {"other"}, {"--yes"}} {
		var output bytes.Buffer
		if code := runHeadlessUpdateWith(t.TempDir(), args, strings.NewReader(""), &output, &output, deps); code != 2 {
			t.Fatal(args, code, output.String())
		}
	}
	if *upgrades != 0 {
		t.Fatal("invalid arguments executed update")
	}
}

func TestHeadlessPackageUpdateRenamedManualBinary(t *testing.T) {
	deps, upgrades := headlessUpdateTestDependencies(t, t.TempDir())
	deps.headless = true
	deps.runtime = func() (packageUpdateRuntime, error) {
		return packageUpdateRuntime{binary: "/tmp/my-headless-program", current: "0.57.0"}, nil
	}
	deps.detect = func(rt packageUpdateRuntime) (packageInstallation, error) {
		return packageInstallation{Method: "manual", Binary: rt.binary}, nil
	}
	var output bytes.Buffer
	if code := runHeadlessUpdateWith(t.TempDir(), []string{"check"}, strings.NewReader(""), &output, &output, deps); code != 0 || *upgrades != 0 || !strings.Contains(output.String(), "Download the new headless package") {
		t.Fatal(code, output.String())
	}
}
