package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeOpenDesignVersionFixture(t *testing.T, executable, platform, content string) string {
	t.Helper()
	resources := filepath.Join(executable, "Contents", "Resources")
	if platform == "windows" {
		resources = filepath.Join(filepath.Dir(executable), "resources")
	}
	if err := os.MkdirAll(resources, 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(resources, "open-design-config.json")
	if err := os.WriteFile(config, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestOpenDesignCompatibilityRequiresReleasedNamespaceContract(t *testing.T) {
	for _, platform := range []string{"macos", "windows"} {
		t.Run(platform, func(t *testing.T) {
			executable := filepath.Join(t.TempDir(), "Open Design.app")
			if platform == "windows" {
				executable = filepath.Join(t.TempDir(), "Open Design.exe")
			}
			if err := openDesignCompatibility(executable, platform); err == nil {
				t.Fatal("missing packaged version was accepted")
			}
			for _, tc := range []struct {
				version string
				want    bool
			}{
				{"0.7.0", false}, {"0.22.1", false}, {"0.22.2-rc.1", false},
				{"0.22.2", true}, {"0.22.2+build.1", true}, {"0.22.3", true}, {"0.23.0-rc.1", true}, {"1.0.0", true},
				{"garbage", false}, {"", false}, {"00.22.2", false}, {"0.22", false}, {"999999999999999999999999.0.0", false},
			} {
				writeOpenDesignVersionFixture(t, executable, platform, `{"appVersion":"`+tc.version+`","unrelated":"metadata"}`)
				err := openDesignCompatibility(executable, platform)
				if (err == nil) != tc.want {
					t.Fatalf("version %q: error=%v", tc.version, err)
				}
				if err != nil && !strings.Contains(err.Error(), "0.22.2") {
					t.Fatal("missing actionable minimum version")
				}
				if platform == "macos" {
					direct := filepath.Join(executable, "Contents", "MacOS", "Open Design")
					if (openDesignCompatibility(direct, platform) == nil) != tc.want {
						t.Fatal("direct bundle executable bypassed the metadata check")
					}
				}
			}
		})
	}
}

func TestOpenDesignCompatibilityRejectsUnsafeMetadata(t *testing.T) {
	for _, kind := range []string{"symlink-file", "symlink-resources", "oversize", "malformed", "missing-version"} {
		t.Run(kind, func(t *testing.T) {
			bundle := filepath.Join(t.TempDir(), "Open Design.app")
			config := writeOpenDesignVersionFixture(t, bundle, "macos", `{"appVersion":"0.22.2"}`)
			switch kind {
			case "symlink-file", "symlink-resources":
				target := filepath.Join(t.TempDir(), "untouched")
				if kind == "symlink-file" {
					if err := os.Rename(config, target); err != nil {
						t.Fatal(err)
					}
				} else {
					config = filepath.Dir(config)
					if err := os.Rename(config, target); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(target, config); err != nil {
					if runtime.GOOS == "windows" {
						t.Skip("symlink privilege unavailable")
					}
					t.Fatal(err)
				}
			case "oversize":
				if err := os.WriteFile(config, []byte(strings.Repeat(" ", int(openDesignPackagedConfigLimit))+`{"appVersion":"0.22.2"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				_ = os.WriteFile(config, []byte(`{"appVersion":"0.22.2"} {}`), 0600)
			case "missing-version":
				_ = os.WriteFile(config, []byte(`{}`), 0600)
			}
			if err := openDesignCompatibility(bundle, "macos"); !errors.Is(err, errOpenDesignCompatibility) {
				t.Fatalf("unsafe metadata accepted: %v", err)
			}
		})
	}
}
