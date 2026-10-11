package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPackageUpdateAPTOwnership(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix package databases")
	}
	for _, test := range []struct {
		name, binary, owner, status string
		uid                         int
		method                      string
		wantError                   bool
	}{
		{"desktop", "/usr/bin/kilo-proxy", "kilo-proxy-desktop: /usr/bin/kilo-proxy", "installed\t0.57.0", 1000, "apt", false},
		{"multiarch", "/usr/bin/kilo-proxy-headless", "kilo-proxy-headless:arm64: /usr/bin/kilo-proxy-headless", "installed\t0.57.0", 1000, "apt", false},
		{"otherowner", "/usr/bin/kilo-proxy", "other-package: /usr/bin/kilo-proxy", "installed\t0.57.0", 1000, "manual", false},
		{"multipleowners", "/usr/bin/kilo-proxy", "kilo-proxy-desktop: /usr/bin/kilo-proxy\nother: /usr/bin/kilo-proxy", "installed\t0.57.0", 1000, "manual", false},
		{"movedcopy", "/tmp/kilo-proxy", "kilo-proxy-desktop: /usr/bin/kilo-proxy", "installed\t0.57.0", 1000, "manual", false},
		{"staleprocess", "/usr/bin/kilo-proxy", "kilo-proxy-desktop: /usr/bin/kilo-proxy", "installed\t0.58.0", 1000, "manual", true},
		{"removedpackage", "/usr/bin/kilo-proxy", "kilo-proxy-desktop: /usr/bin/kilo-proxy", "config-files\t0.57.0", 1000, "manual", true},
		{"root", "/usr/bin/kilo-proxy", "kilo-proxy-desktop: /usr/bin/kilo-proxy", "installed\t0.57.0", 0, "manual", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			rt := packageUpdateRuntime{platform: "linux", home: t.TempDir(), binary: test.binary, current: "0.57.0", uid: test.uid, fingerprint: func(string) (string, error) { return strings.Repeat("a", 64), nil }, capture: func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "/usr/bin/dpkg-query" {
					return nil, errors.New("not installed")
				}
				if args[0] == "-S" {
					return []byte(test.owner), nil
				}
				return []byte(test.status), nil
			}}
			installation, err := detectPackageInstallation(rt)
			if installation.Method != test.method || (err != nil) != test.wantError {
				t.Fatal(installation, err)
			}
			if test.method == "apt" && (installation.Manager != "/usr/bin/apt-get" || installation.Binary != test.binary || installation.Relaunch != test.binary) {
				t.Fatal(installation)
			}
		})
	}
}

func TestPackageUpdateBrewOwnershipAndManualCopies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix package databases")
	}
	for _, cask := range []bool{false, true} {
		for _, kind := range []string{"owned", "manual-copy", "wrong-tap", "wrong-version", "receipt-symlink"} {
			t.Run(fmtPackageTest(cask, kind), func(t *testing.T) {
				home, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				prefix := filepath.Join(home, ".linuxbrew")
				brew := filepath.Join(prefix, "bin/brew")
				if err := os.MkdirAll(filepath.Dir(brew), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(brew, []byte("synthetic brew"), 0700); err != nil {
					t.Fatal(err)
				}
				binary := filepath.Join(prefix, "Cellar/kilo-proxy-desktop/0.57.0/bin/kilo-proxy")
				platform := "linux"
				if cask {
					platform = "darwin"
					binary = filepath.Join(home, "Applications/Kilo Proxy.app/Contents/MacOS/kilo-proxy")
				}
				if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(binary, []byte("synthetic installed app"), 0700); err != nil {
					t.Fatal(err)
				}
				tap, installed := "rosseca/tap", "0.57.0"
				if kind == "wrong-tap" {
					tap = "other/tap"
				}
				if kind == "wrong-version" {
					installed = "0.58.0"
				}
				receipt := filepath.Join(prefix, "Cellar/kilo-proxy-desktop/0.57.0/INSTALL_RECEIPT.json")
				if !cask {
					if err := os.WriteFile(receipt, []byte(`{"source":{"tap":"`+tap+`","spec":"stable"}}`), 0600); err != nil {
						t.Fatal(err)
					}
					if kind == "receipt-symlink" {
						actual := receipt + ".real"
						_ = os.Rename(receipt, actual)
						_ = os.Symlink(actual, receipt)
					}
				}
				original := binary
				if kind == "manual-copy" {
					binary = filepath.Join(home, "manual/kilo-proxy")
					_ = os.MkdirAll(filepath.Dir(binary), 0700)
					_ = os.WriteFile(binary, []byte("copy"), 0700)
				}
				rt := packageUpdateRuntime{platform: platform, home: home, binary: binary, current: "0.57.0", uid: 1000, capture: func(_ context.Context, name string, args ...string) ([]byte, error) {
					if name != brew {
						return nil, errors.New("not this brew")
					}
					switch args[0] {
					case "--prefix":
						return []byte(prefix), nil
					case "--cellar":
						return []byte(filepath.Join(prefix, "Cellar/kilo-proxy-desktop")), nil
					case "info":
						return []byte(`{"casks":[{"token":"kilo-proxy","tap":"` + tap + `","installed":"` + installed + `"}]}`), nil
					case "list":
						if cask {
							return []byte("==> App\n" + strings.TrimSuffix(original, "/Contents/MacOS/kilo-proxy") + " (4 files, 15MB)\n"), nil
						}
						return []byte("kilo-proxy-desktop " + installed), nil
					}
					return nil, errors.New("unexpected probe")
				}}
				installation, err := detectPackageInstallation(rt)
				owned := kind == "owned" || cask && kind == "receipt-symlink"
				if err != nil || (installation.Method != "manual") != owned {
					t.Fatal(installation, err)
				}
				if owned && installation.Binary != original {
					t.Fatal("wrong installation", installation)
				}
			})
		}
	}
}

func fmtPackageTest(cask bool, kind string) string {
	if cask {
		return "cask/" + kind
	}
	return "formula/" + kind
}

func TestPackageUpdateExactCommandsFeedLagAndVerification(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix package managers")
	}
	for _, method := range []string{"apt", "brew-cask", "brew-formula"} {
		for _, failure := range []string{"", "catalog", "feed-lag", "install", "old-binary"} {
			t.Run(method+"/"+failure, func(t *testing.T) {
				in := packageInstallation{Method: method, Manager: "/example/bin/brew", Package: "rosseca/tap/kilo-proxy-desktop", Binary: "/usr/bin/kilo-proxy", Relaunch: "/example/opt/kilo-proxy-desktop/bin/kilo-proxy"}
				if method == "apt" {
					in.Manager, in.Package, in.Relaunch = "/usr/bin/apt-get", "kilo-proxy-desktop", in.Binary
				}
				if method == "brew-cask" {
					in.Package = "rosseca/tap/kilo-proxy"
				}
				var commands [][]string
				run := func(_ context.Context, command string, args []string, capture bool) ([]byte, error) {
					commands = append(commands, append([]string{command}, args...))
					if args[0] == "update" || command == "/usr/bin/sudo" && args[1] == "update" {
						if failure == "catalog" {
							return nil, errors.New("fail")
						}
						return nil, nil
					}
					if args[0] == "policy" {
						if failure == "feed-lag" {
							return []byte("Candidate: 0.57.0"), nil
						}
						return []byte("kilo-proxy-desktop:\n  Installed: 0.57.0\n  Candidate: 0.58.0\n  Version table:\n     0.58.0 500\n        500 https://rosseca.github.io/kilo-proxy/apt/ noble/main arm64 Packages\n *** 0.57.0 100\n        100 /var/lib/dpkg/status\n"), nil
					}
					if args[0] == "info" {
						candidate := "0.58.0"
						if failure == "feed-lag" {
							candidate = "0.57.0"
						}
						if method == "brew-cask" {
							return []byte(`{"casks":[{"token":"kilo-proxy","tap":"rosseca/tap","version":"` + candidate + `"}]}`), nil
						}
						return []byte(`{"formulae":[{"full_name":"rosseca/tap/kilo-proxy-desktop","versions":{"stable":"` + candidate + `"}}]}`), nil
					}
					if args[0] == "upgrade" || command == "/usr/bin/sudo" && args[1] == "install" {
						if failure == "install" {
							return nil, errors.New("fail")
						}
						return nil, nil
					}
					if args[0] == "--version" {
						if failure == "old-binary" {
							return []byte("0.57.0"), nil
						}
						return []byte("0.58.0\n"), nil
					}
					t.Fatal("unexpected command", command, args)
					return nil, nil
				}
				var output bytes.Buffer
				_, err := executePackageUpgradeWith(context.Background(), in, "0.58.0", nil, &output, &output, run)
				if (err != nil) != (failure != "") {
					t.Fatal(commands, err)
				}
				for _, command := range commands {
					joined := strings.Join(command, " ")
					if strings.Contains(joined, "dist-upgrade") || strings.Contains(joined, " upgrade ") && !strings.Contains(joined, in.Package) || strings.Contains(joined, "install ") && !strings.Contains(joined, in.Package+"=0.58.0") {
						t.Fatal("unscoped upgrade", command)
					}
				}
				if failure == "feed-lag" {
					for _, command := range commands {
						if strings.Contains(strings.Join(command, " "), "upgrade") || strings.Contains(strings.Join(command, " "), "install") {
							t.Fatal("installed before feed ready", commands)
						}
					}
				}
				if method == "apt" && failure == "" && !reflect.DeepEqual(commands[2], []string{"/usr/bin/sudo", "/usr/bin/apt-get", "install", "--only-upgrade", "kilo-proxy-desktop=0.58.0"}) {
					t.Fatal(commands)
				}
			})
		}
	}
}

func TestPackageUpdateAPTCandidateRejectsOtherOriginsAndVersions(t *testing.T) {
	for _, policy := range []string{"0.58.0 500\n 500 https://evil.example/apt noble/main arm64 Packages", "0.57.0 500\n 500 https://rosseca.github.io/kilo-proxy/apt noble/main arm64 Packages", "0.58.0 500\n 500 https://rosseca.github.io/kilo-proxy/apt.evil noble/main arm64 Packages", "0.58.0 990\n 990 https://other.example/apt noble/main amd64 Packages\n 500 https://rosseca.github.io/kilo-proxy/apt noble/main amd64 Packages", "0.58.0 990\n 500 https://rosseca.github.io/kilo-proxy/apt noble/main amd64 Packages\n 990 https://other.example/apt noble/main amd64 Packages"} {
		if packageUpdateAPTCandidate(policy, "0.58.0") {
			t.Fatal(policy)
		}
	}
}

func TestPackageUpdateEnvironmentAndPlanValidation(t *testing.T) {
	t.Setenv("KILO_API_KEY", "secret")
	t.Setenv("ANTHROPIC_API_KEY", "secret")
	t.Setenv("NODE_OPTIONS", "--import bad")
	t.Setenv("LD_PRELOAD", "bad")
	env := packageUpdateEnvironment("/home/example")
	for _, value := range env {
		if strings.Contains(value, "secret") || strings.HasPrefix(value, "NODE_OPTIONS=") || strings.HasPrefix(value, "LD_PRELOAD=") {
			t.Fatal(value)
		}
	}
	plan := packageUpdatePlan{Version: 1, Expires: time.Now().Add(time.Minute).Unix(), Profile: "/home/example/profile", Home: "/home/example", Platform: runtime.GOOS, Target: "0.58.0", Token: strings.Repeat("a", 64), Environment: env, RestartEnvironment: []string{"HOME=/home/example", "PATH=" + packageUpdateManagerPath}, Installation: packageInstallation{Binary: "/usr/bin/kilo-proxy", Relaunch: "/usr/bin/kilo-proxy", Manager: "/usr/bin/apt-get", Version: "0.57.0", Digest: strings.Repeat("b", 64)}}
	if runtime.GOOS == "windows" {
		if validatePackageUpdatePlan(plan) == nil {
			t.Fatal("Windows plan allowed")
		}
		return
	}
	if err := validatePackageUpdatePlan(plan); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*packageUpdatePlan){func(p *packageUpdatePlan) { p.Expires = time.Now().Add(-time.Second).Unix() }, func(p *packageUpdatePlan) { p.Target = "0.58.0-beta.1" }, func(p *packageUpdatePlan) { p.Args = []string{"--terminal-agent"} }, func(p *packageUpdatePlan) { p.Environment = append(p.Environment, "NODE_OPTIONS=bad") }, func(p *packageUpdatePlan) { p.Environment = append(p.Environment, "HOME=/elsewhere") }, func(p *packageUpdatePlan) {
		p.Environment = []string{"HOME=/home/example", "PATH=/home/example/.nvm/bin"}
	}, func(p *packageUpdatePlan) { p.RestartEnvironment = append(p.RestartEnvironment, "NODE_OPTIONS=bad") }, func(p *packageUpdatePlan) {
		p.RestartEnvironment = append(p.RestartEnvironment, "GIT_CONFIG_GLOBAL=/elsewhere")
	}, func(p *packageUpdatePlan) { p.RestartEnvironment = []string{"HOME=/home/example", "PATH=.:/usr/bin"} }} {
		invalid := plan
		mutate(&invalid)
		if validatePackageUpdatePlan(invalid) == nil {
			t.Fatal("invalid plan accepted", invalid)
		}
	}
	t.Setenv("PATH", "/home/example/.nvm/versions/node/v22/bin:/home/example/.asdf/shims:/usr/bin:/bin")
	restart, err := packageUpdateRestartEnvironment("/home/example")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range restart {
		if strings.HasPrefix(entry, "HOMEBREW_") || strings.HasPrefix(entry, "GIT_CONFIG_") || strings.Contains(entry, "secret") || strings.HasPrefix(entry, "NODE_OPTIONS=") || strings.HasPrefix(entry, "LD_PRELOAD=") {
			t.Fatal("restart environment leaked manager flags or credentials", entry)
		}
	}
	if !strings.Contains(strings.Join(restart, "\n"), "PATH="+os.Getenv("PATH")) || !strings.Contains(strings.Join(packageUpdateEnvironment("/home/example"), "\n"), "PATH="+packageUpdateManagerPath) {
		t.Fatal("manager and restart paths were not separated")
	}
	for _, path := range []string{"/usr/bin:", ":/usr/bin", "/usr/bin:relative", "/usr/bin:\n/tmp"} {
		t.Setenv("PATH", path)
		if _, err = packageUpdateRestartEnvironment("/home/example"); err == nil {
			t.Fatal("unsafe restart PATH accepted", path)
		}
	}
}

func TestPackageUpdateWrapperRefreshPreservesUnrelatedProfilesAndShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix wrappers")
	}
	home := t.TempDir()
	profile := filepath.Join(home, "profile")
	old, newBinary := "/old/version/bin/kilo-proxy", "/stable/opt/bin/kilo-proxy"
	_ = os.MkdirAll(filepath.Join(home, ".local/bin"), 0700)
	owned := filepath.Join(home, ".local/bin/kilo-codex")
	foreign := filepath.Join(home, ".local/bin/kilo-claude")
	foreignData := terminalCommandScript(old, filepath.Join(home, "another-profile"), "claude")
	_ = os.WriteFile(owned, terminalCommandScript(old, profile, "codex-cli"), 0700)
	_ = os.WriteFile(foreign, foreignData, 0700)
	_ = os.WriteFile(filepath.Join(home, ".zshrc"), []byte("unrelated shell content"), 0600)
	if err := refreshPackageUpdateWrappers(home, profile, old, newBinary); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(owned)
	if !bytes.Equal(data, terminalCommandScript(newBinary, profile, "codex-cli")) {
		t.Fatal(string(data))
	}
	data, _ = os.ReadFile(foreign)
	if !bytes.Equal(data, foreignData) {
		t.Fatal("changed another profile")
	}
	if _, err := os.Stat(filepath.Join(home, ".local/bin/kilo-omp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created new wrapper")
	}
	data, _ = os.ReadFile(filepath.Join(home, ".zshrc"))
	if string(data) != "unrelated shell content" {
		t.Fatal("modified shell")
	}
}

func TestPackageUpdateAPIRequiresAuthConfirmationAndCurrentStable(t *testing.T) {
	a := &app{adminHost: "127.0.0.1:9000", adminToken: "admin", quit: make(chan struct{}), updates: newReleaseUpdateChecker("0.57.0")}
	defer a.updates.close()
	a.updates.state = releaseUpdateState{CurrentVersion: "0.57.0", LatestVersion: "0.58.0", Available: true, ReleaseURL: releaseUpdatePagePrefix + "v0.58.0"}
	for _, test := range []struct {
		body, auth string
		status     int
	}{{`{"version":"0.58.0","confirm":true}`, "", 401}, {`{"version":"0.58.0","confirm":false}`, "Bearer admin", 409}, {`{"version":"0.57.0","confirm":true}`, "Bearer admin", 409}, {`{"version":"0.58.0","confirm":true,"command":"brew upgrade"}`, "Bearer admin", 400}, {`{"version":"0.58.0","confirm":true}`, "Bearer admin", 409}} {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9000/api/updates/install", strings.NewReader(test.body))
		r.Header.Set("Authorization", test.auth)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.adminHandler().ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestPackageUpdateProfileWaitNeverSignalsAnotherProcess(t *testing.T) {
	dir := t.TempDir()
	unlock, err := acquireProfileLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if release, err := packageUpdateWaitProfile(dir, 150*time.Millisecond); err == nil {
		release()
		t.Fatal("acquired active profile")
	}
	unlock()
	release, err := packageUpdateWaitProfile(dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestPackageUpdateHelperSubprocess(t *testing.T) {
	if os.Getenv("KILO_UPDATE_HELPER_TEST") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Exit(runPackageUpdateMode(os.Args[i+1:]))
		}
	}
	os.Exit(2)
}

func TestPackageUpdateRealHelperHandshakeLockUpgradeAndRelaunch(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failed-upgrade"}[fail], func(t *testing.T) { runPackageUpdateHelperFixture(t, fail) })
	}
}

func runPackageUpdateHelperFixture(t *testing.T, failUpgrade bool) {
	if runtime.GOOS == "windows" {
		t.Skip("Manual Windows updater")
	}
	if os.Geteuid() == 0 {
		t.Skip("Updater intentionally refuses root")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(home, "profile")
	_ = os.Mkdir(profile, 0700)
	prefix := filepath.Join(home, ".linuxbrew")
	brew := filepath.Join(prefix, "bin/brew")
	_ = os.MkdirAll(filepath.Dir(brew), 0700)
	binary := filepath.Join(prefix, "Cellar/kilo-proxy-desktop/0.57.0/bin/kilo-proxy")
	if runtime.GOOS == "darwin" {
		binary = filepath.Join(home, "Applications/Kilo Proxy.app/Contents/MacOS/kilo-proxy")
	}
	_ = os.MkdirAll(filepath.Dir(binary), 0700)
	self, _ := os.Executable()
	source, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := os.OpenFile(binary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(destination, source)
	_ = source.Close()
	_ = destination.Close()
	if err != nil {
		t.Fatal(err)
	}
	versionFile, log, launch := filepath.Join(home, "version"), filepath.Join(home, "manager.log"), filepath.Join(home, "relaunch.log")
	managerPathLog, restartPathLog, nodeLog, secretLog := filepath.Join(home, "manager-path.log"), filepath.Join(home, "restart-path.log"), filepath.Join(home, "node.log"), filepath.Join(home, "secret.log")
	nodeBin := filepath.Join(home, ".nvm/versions/node/v22/bin")
	_ = os.MkdirAll(nodeBin, 0700)
	_ = os.WriteFile(filepath.Join(nodeBin, "node"), []byte("#!/bin/sh\necho synthetic-node-ready\n"), 0700)
	t.Setenv("PATH", nodeBin+":/usr/bin:/bin")
	t.Setenv("KILO_API_KEY", "synthetic-secret")
	t.Setenv("NODE_OPTIONS", "synthetic-injection")
	_ = os.WriteFile(versionFile, []byte("0.57.0"), 0600)
	newSource := filepath.Join(home, "new-program")
	newScript := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 0.58.0; else printf '%s\\n' \"$PATH\" > " + helperShellQuote(restartPathLog) + "; /usr/bin/env node > " + helperShellQuote(nodeLog) + "; if [ -n \"${KILO_API_KEY-}${NODE_OPTIONS-}${HOMEBREW_NO_AUTO_UPDATE-}${GIT_CONFIG_GLOBAL-}\" ]; then printf leaked > " + helperShellQuote(secretLog) + "; fi; printf '%s\\n' \"$@\" > " + helperShellQuote(launch) + "; fi\n"
	_ = os.WriteFile(newSource, []byte(newScript), 0700)
	bundle := strings.TrimSuffix(binary, "/Contents/MacOS/kilo-proxy")
	brewScript := "#!/bin/sh\nprintf '%s\\n' \"$PATH\" >> " + helperShellQuote(managerPathLog) + "; printf '%s\\n' \"$*\" >> " + helperShellQuote(log) + "\nv=$(/bin/cat " + helperShellQuote(versionFile) + ")\ncase \"$1\" in\n--prefix) printf '%s\\n' " + helperShellQuote(prefix) + ";;\n--cellar) printf '%s\\n' " + helperShellQuote(filepath.Join(prefix, "Cellar/kilo-proxy-desktop")) + ";;\ninfo) "
	if runtime.GOOS == "darwin" {
		brewScript += "printf '{\"casks\":[{\"token\":\"kilo-proxy\",\"tap\":\"rosseca/tap\",\"installed\":\"%s\",\"version\":\"0.58.0\"}]}\\n' \"$v\";;\nlist) printf '%s (4 files, 15MB)\\n' " + helperShellQuote(bundle) + ";;\n"
	} else {
		brewScript += "printf '{\"formulae\":[{\"full_name\":\"rosseca/tap/kilo-proxy-desktop\",\"versions\":{\"stable\":\"0.58.0\"}}]}\\n';;\nlist) printf 'kilo-proxy-desktop %s\\n' \"$v\";;\n"
		receipt := filepath.Join(prefix, "Cellar/kilo-proxy-desktop/0.57.0/INSTALL_RECEIPT.json")
		_ = os.WriteFile(receipt, []byte(`{"source":{"tap":"rosseca/tap","spec":"stable"}}`), 0600)
	}
	brewScript += "update) :;;\nupgrade) "
	if failUpgrade {
		brewScript += "exit 19; "
	}
	if runtime.GOOS == "darwin" {
		brewScript += "/bin/cp " + helperShellQuote(newSource) + " " + helperShellQuote(binary) + "; "
	} else {
		newRoot := filepath.Join(prefix, "Cellar/kilo-proxy-desktop/0.58.0")
		opt := filepath.Join(prefix, "opt/kilo-proxy-desktop")
		brewScript += "/bin/mkdir -p " + helperShellQuote(filepath.Join(newRoot, "bin")) + " " + helperShellQuote(filepath.Dir(opt)) + "; /bin/cp " + helperShellQuote(newSource) + " " + helperShellQuote(filepath.Join(newRoot, "bin/kilo-proxy")) + "; /bin/cp " + helperShellQuote(filepath.Join(prefix, "Cellar/kilo-proxy-desktop/0.57.0/INSTALL_RECEIPT.json")) + " " + helperShellQuote(filepath.Join(newRoot, "INSTALL_RECEIPT.json")) + "; /bin/ln -sfn " + helperShellQuote(newRoot) + " " + helperShellQuote(opt) + "; /bin/rm -rf " + helperShellQuote(filepath.Join(prefix, "Cellar/kilo-proxy-desktop/0.57.0")) + "; "
	}
	brewScript += "printf 0.58.0 > " + helperShellQuote(versionFile) + ";;\n*) exit 2;;\nesac\n"
	_ = os.WriteFile(brew, []byte(brewScript), 0700)
	rt := packageUpdateRuntime{platform: runtime.GOOS, home: home, binary: binary, current: "0.57.0", uid: os.Geteuid(), capture: packageUpdateCapture(home)}
	in, err := detectPackageInstallation(rt)
	if err != nil || in.Method == "manual" {
		t.Fatal(in, err)
	}
	restartEnvironment, err := packageUpdateRestartEnvironment(home)
	if err != nil {
		t.Fatal(err)
	}
	plan := packageUpdatePlan{Version: 1, Expires: time.Now().Add(time.Minute).Unix(), Installation: in, Profile: profile, Home: home, Platform: runtime.GOOS, Target: "0.58.0", Token: strings.Repeat("c", 64), Environment: packageUpdateEnvironment(home), RestartEnvironment: restartEnvironment}
	root, helper, ticket, err := writePackageUpdatePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	unlock, err := acquireProfileLock(profile)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	command := exec.Command(helper, "-test.run=^TestPackageUpdateHelperSubprocess$", "--", ticket)
	command.Env = append(plan.Environment, "KILO_UPDATE_HELPER_TEST=1")
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	if err = packageUpdateWaitMarker(root, "ready", plan.Token, 15*time.Second); err != nil {
		t.Fatal(err, output.String())
	}
	if _, err = os.Stat(ticket); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ticket not consumed", err)
	}
	if _, err = readPackageUpdatePlan(ticket); err == nil {
		t.Fatal("ticket replay accepted")
	}
	if err = os.WriteFile(filepath.Join(root, "proceed"), []byte(plan.Token), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	data, _ := os.ReadFile(log)
	if strings.Contains(string(data), "upgrade") || strings.Contains(string(data), "\nupdate\n") {
		t.Fatal("manager ran while app still held profile", string(data))
	}
	unlock()
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	select {
	case err = <-finished:
		if (err != nil) != failUpgrade {
			t.Fatal(err, output.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatal("update helper did not finish")
	}
	if failUpgrade {
		if _, err = os.Stat(launch); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("reopened after failed upgrade", err)
		}
		data, err = os.ReadFile(filepath.Join(profile, packageUpdateResultFile))
		if err != nil || !strings.Contains(string(data), `"failed":true`) || !strings.Contains(output.String(), "not restarted") {
			t.Fatal(string(data), err, output.String())
		}
		if _, err = os.Stat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("failed helper not cleaned", err)
		}
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err = os.ReadFile(launch); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if string(data) != "--config-dir\n"+profile+"\n" {
		t.Fatal("wrong relaunch args", string(data), output.String())
	}
	data, err = os.ReadFile(restartPathLog)
	if err != nil || strings.TrimSpace(string(data)) != os.Getenv("PATH") {
		t.Fatal("restart lost the original CLI PATH", string(data), err)
	}
	data, err = os.ReadFile(nodeLog)
	if err != nil || strings.TrimSpace(string(data)) != "synthetic-node-ready" {
		t.Fatal("env node did not resolve through the restored session PATH", string(data), err)
	}
	data, err = os.ReadFile(managerPathLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if path != packageUpdateManagerPath {
			t.Fatal("package manager inherited the session PATH", path)
		}
	}
	if _, err = os.Stat(secretLog); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("restart inherited a credential, injection variable or manager-only flag", err)
	}
	data, _ = os.ReadFile(log)
	if !strings.Contains(string(data), "upgrade") || !strings.Contains(output.String(), "Started Kilo Proxy with your existing profile") {
		t.Fatal(string(data), output.String())
	}
	if _, err = os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("private helper not cleaned", err)
	}
}

func TestPackageUpdateManagerFailedHandoffAndDuplicate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix package manager handoff")
	}
	for _, terminalFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-ack", true: "terminal-failed"}[terminalFails], func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			profile := filepath.Join(home, "profile")
			_ = os.Mkdir(profile, 0700)
			prefix := filepath.Join(home, ".linuxbrew")
			brew := filepath.Join(prefix, "bin/brew")
			_ = os.MkdirAll(filepath.Dir(brew), 0700)
			_ = os.WriteFile(brew, []byte("synthetic brew"), 0700)
			binary := filepath.Join(prefix, "Cellar/kilo-proxy-desktop/0.57.0/bin/kilo-proxy")
			_ = os.MkdirAll(filepath.Dir(binary), 0700)
			_ = os.WriteFile(binary, []byte("synthetic source"), 0700)
			_ = os.WriteFile(filepath.Join(prefix, "Cellar/kilo-proxy-desktop/0.57.0/INSTALL_RECEIPT.json"), []byte(`{"source":{"tap":"rosseca/tap","spec":"stable"}}`), 0600)
			rt := packageUpdateRuntime{platform: "linux", home: home, binary: binary, current: "0.57.0", uid: 1000, capture: func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != brew {
					return nil, errors.New("not this brew")
				}
				switch args[0] {
				case "--prefix":
					return []byte(prefix), nil
				case "--cellar":
					return []byte(filepath.Join(prefix, "Cellar/kilo-proxy-desktop")), nil
				case "list":
					return []byte("kilo-proxy-desktop 0.57.0"), nil
				}
				return nil, errors.New("unexpected")
			}}
			in, err := detectPackageInstallation(rt)
			if err != nil || in.Method == "manual" {
				t.Fatal(in, err)
			}
			// Plan platform is the executing host; use a formula fixture even on
			// macOS here so the source ownership probe remains entirely synthetic.
			rt.platform = runtime.GOOS
			if runtime.GOOS == "darwin" {
				// Formula headless is supported on macOS; no server is started.
				newBinary := filepath.Join(prefix, "Cellar/kilo-proxy-headless/0.57.0/bin/kilo-proxy-headless")
				_ = os.MkdirAll(filepath.Dir(newBinary), 0700)
				_ = os.WriteFile(newBinary, []byte("synthetic source"), 0700)
				_ = os.WriteFile(filepath.Join(prefix, "Cellar/kilo-proxy-headless/0.57.0/INSTALL_RECEIPT.json"), []byte(`{"source":{"tap":"rosseca/tap","spec":"stable"}}`), 0600)
				rt.binary = newBinary
				rt.capture = func(_ context.Context, name string, args ...string) ([]byte, error) {
					if name != brew {
						return nil, errors.New("other brew")
					}
					switch args[0] {
					case "--prefix":
						return []byte(prefix), nil
					case "--cellar":
						return []byte(filepath.Join(prefix, "Cellar/kilo-proxy-headless")), nil
					case "list":
						return []byte("kilo-proxy-headless 0.57.0"), nil
					}
					return nil, errors.New("unexpected")
				}
				in, err = detectPackageInstallation(rt)
				if err != nil {
					t.Fatal(err)
				}
			}
			var quits, starts atomic.Int32
			var root string
			manager := &packageUpdateManager{rt: rt, profile: profile, checked: true, installation: in, readyTimeout: 200 * time.Millisecond, done: make(chan struct{}), quit: func() { quits.Add(1) }, terminal: func(_, ticket, _ string, _ []string) error {
				starts.Add(1)
				root = filepath.Dir(ticket)
				if terminalFails {
					return errors.New("no terminal")
				}
				return nil
			}}
			err = manager.start("0.58.0")
			if (err != nil) != terminalFails {
				t.Fatal(err)
			}
			if !terminalFails {
				if err = manager.start("0.58.0"); err == nil {
					t.Fatal("duplicate updater accepted")
				}
				deadline := time.Now().Add(time.Second)
				for time.Now().Before(deadline) {
					var state releaseUpdateState
					manager.snapshot(&state)
					if !state.Installing {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			var state releaseUpdateState
			manager.snapshot(&state)
			if state.Installing || state.InstallMessage != "update_start_failed" || quits.Load() != 0 || starts.Load() != 1 {
				t.Fatal(state, quits.Load(), starts.Load())
			}
			if _, err = os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed preparation left helper files", err)
			}
		})
	}
}

func TestPackageUpdateManagerCancelledBeforeTerminal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix installation paths")
	}
	for _, alreadyCancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "during-ownership-probe", true: "before-preparation"}[alreadyCancelled], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var cancelProbe bool
			rt := packageUpdateRuntime{platform: "linux", home: "/home/synthetic", binary: "/usr/bin/kilo-proxy", current: "0.57.0", uid: 1000,
				fingerprint: func(string) (string, error) { return strings.Repeat("a", 64), nil },
				capture: func(probe context.Context, name string, args ...string) ([]byte, error) {
					if cancelProbe {
						cancel()
						select {
						case <-probe.Done():
							return nil, probe.Err()
						default:
							t.Fatal("ownership probe did not inherit the request context")
						}
					}
					if name != "/usr/bin/dpkg-query" {
						return nil, errors.New("unexpected package manager")
					}
					if args[0] == "-S" {
						return []byte("kilo-proxy-desktop: /usr/bin/kilo-proxy"), nil
					}
					return []byte("installed\t0.57.0"), nil
				}}
			in, err := detectPackageInstallation(rt)
			if err != nil || in.Method != "apt" {
				t.Fatal(in, err)
			}
			var starts, quits int
			manager := &packageUpdateManager{rt: rt, checked: true, installation: in, message: "managed_installation", quit: func() { quits++ }, terminal: func(string, string, string, []string) error { starts++; return nil }}
			if alreadyCancelled {
				cancel()
			} else {
				cancelProbe = true
			}
			if err = manager.startWithContext(ctx, "0.58.0"); err == nil {
				t.Fatal("cancelled update accepted")
			}
			var state releaseUpdateState
			manager.snapshot(&state)
			if starts != 0 || quits != 0 || state.Installing || !state.CanInstall {
				t.Fatal("cancelled preparation changed the running app", starts, quits, state)
			}
			if !alreadyCancelled && state.InstallMessage != "update_start_failed" {
				t.Fatal("failed preparation state was not restored", state)
			}
		})
	}
}
