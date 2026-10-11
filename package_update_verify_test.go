package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPackageUpdateVerifiesNewFormulaOwnership(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Package-manager updates are supported on macOS and Linux.")
	}
	for _, state := range []string{"valid", "different-tap", "different-package", "old-version", "missing-launch-path", "redirected-launch-path"} {
		t.Run(state, func(t *testing.T) {
			home := t.TempDir()
			home, err := filepath.EvalSymlinks(home)
			if err != nil {
				t.Fatal(err)
			}
			prefix := filepath.Join(home, ".linuxbrew")
			brew := filepath.Join(prefix, "bin", "brew")
			cellar := filepath.Join(prefix, "Cellar", "kilo-proxy-headless")
			keg := filepath.Join(cellar, "0.58.0")
			binary := filepath.Join(keg, "bin", "kilo-proxy-headless")
			opt := filepath.Join(prefix, "opt", "kilo-proxy-headless")
			for _, directory := range []string{filepath.Dir(brew), filepath.Dir(binary), filepath.Dir(opt)} {
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(brew, []byte("synthetic manager; never executed"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(binary, []byte("synthetic new executable; never executed"), 0700); err != nil {
				t.Fatal(err)
			}
			receipt := `{"source":{"tap":"rosseca/tap","spec":"stable"}}`
			if state == "different-tap" {
				receipt = `{"source":{"tap":"other/tap","spec":"stable"}}`
			}
			if err := os.WriteFile(filepath.Join(keg, "INSTALL_RECEIPT.json"), []byte(receipt), 0600); err != nil {
				t.Fatal(err)
			}
			if state != "missing-launch-path" {
				linkTarget := keg
				if state == "redirected-launch-path" {
					linkTarget = filepath.Join(home, "unowned")
					if err := os.MkdirAll(filepath.Join(linkTarget, "bin"), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(linkTarget, "bin", "kilo-proxy-headless"), []byte("other binary"), 0700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(linkTarget, opt); err != nil {
					t.Fatal(err)
				}
			}
			launch := filepath.Join(opt, "bin", "kilo-proxy-headless")
			rt := packageUpdateRuntime{platform: "linux", home: home, uid: 1000, current: "0.57.0"}
			rt.capture = func(_ context.Context, command string, args ...string) ([]byte, error) {
				if command == launch && strings.Join(args, " ") == "--version" {
					if state == "old-version" {
						return []byte("0.57.0\n"), nil
					}
					return []byte("0.58.0\n"), nil
				}
				if command != brew {
					return nil, errors.New("unexpected manager")
				}
				switch strings.Join(args, " ") {
				case "--prefix":
					return []byte(prefix), nil
				case "--cellar rosseca/tap/kilo-proxy-headless":
					return []byte(cellar), nil
				case "list --versions rosseca/tap/kilo-proxy-headless":
					return []byte("kilo-proxy-headless 0.58.0"), nil
				}
				return nil, errors.New("unexpected probe")
			}
			old := packageInstallation{Method: "brew-formula", Manager: brew, Package: "rosseca/tap/kilo-proxy-headless", Version: "0.57.0"}
			if state == "different-package" {
				old.Package = "rosseca/tap/kilo-proxy-desktop"
			}
			err = verifyPackageUpdateOwnership(rt, old, launch, "0.58.0")
			if (err == nil) != (state == "valid") {
				t.Fatal(state, err)
			}
		})
	}
}
