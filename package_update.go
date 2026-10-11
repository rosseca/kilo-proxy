package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const packageUpdateRunnerFlag = "--run-package-update"
const packageUpdateResultFile = "package-update-result.json"
const packageUpdateOutputLimit = 1 << 20
const packageUpdateManagerPath = "/opt/homebrew/bin:/usr/local/bin:/home/linuxbrew/.linuxbrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"

type packageInstallation struct {
	Method, Manager, Package, Binary, Relaunch, Prefix, Version, Digest string
}

type packageUpdateRuntime struct {
	platform, home, binary, current string
	uid                             int
	capture                         func(context.Context, string, ...string) ([]byte, error)
	fingerprint                     func(string) (string, error) // Synthetic package-database tests only.
	context                         context.Context              // Preparation may be cancelled by its HTTP caller.
}

// Only session information needed by a terminal, package manager and the
// relaunched desktop is inherited. Provider credentials and code injection
// variables (LD_*, DYLD_*, NODE_OPTIONS, etc.) never enter the update runner.
func packageUpdateEnvironment(home string) []string {
	env := []string{"HOME=" + home, "PATH=" + packageUpdateManagerPath, "HOMEBREW_NO_ANALYTICS=1", "HOMEBREW_NO_AUTO_UPDATE=1", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	return appendPackageUpdateSessionEnvironment(env)
}

func appendPackageUpdateSessionEnvironment(env []string) []string {
	for _, name := range []string{"USER", "LOGNAME", "LANG", "LC_ALL", "LC_CTYPE", "DISPLAY", "WAYLAND_DISPLAY", "DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_DATA_DIRS", "SHELL", "ZDOTDIR", "TERM", "COLORTERM"} {
		if value := os.Getenv(name); value != "" && len(value) <= 8192 && !strings.ContainsAny(value, "\x00\r\n") {
			env = append(env, name+"="+value)
		}
	}
	return env
}

// The updated desktop still needs the user's CLI discovery paths (nvm/asdf,
// for example). Keep that PATH separate from every package-manager process.
func packageUpdateRestartEnvironment(home string) ([]string, error) {
	path := os.Getenv("PATH")
	if path == "" {
		path = packageUpdateManagerPath
	}
	if !validPackageUpdateRestartPath(path) {
		return nil, errors.New("The session PATH must contain only absolute directories before updating.")
	}
	return appendPackageUpdateSessionEnvironment([]string{"HOME=" + home, "PATH=" + path}), nil
}

func validPackageUpdateRestartPath(path string) bool {
	if path == "" || len(path) > 32768 || strings.ContainsAny(path, "\x00\r\n") {
		return false
	}
	for _, directory := range filepath.SplitList(path) {
		if !filepath.IsAbs(directory) || len(directory) > 4096 {
			return false
		}
	}
	return true
}

type packageUpdateBuffer struct {
	mu       sync.Mutex
	data     []byte
	overflow bool
}

func (b *packageUpdateBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if len(b.data)+n > packageUpdateOutputLimit {
		b.overflow = true
		p = p[:packageUpdateOutputLimit-len(b.data)]
	}
	b.data = append(b.data, p...)
	return n, nil
}

func packageUpdateCapture(home string) func(context.Context, string, ...string) ([]byte, error) {
	return func(ctx context.Context, command string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, command, args...)
		cmd.WaitDelay = 2 * time.Second
		cmd.Env = packageUpdateEnvironment(home)
		var output packageUpdateBuffer
		cmd.Stdout, cmd.Stderr = &output, io.Discard
		err := cmd.Run()
		if output.overflow {
			return nil, errors.New("The package manager response exceeded the size limit.")
		}
		return output.data, err
	}
}

func newPackageUpdateRuntime(current string) (packageUpdateRuntime, error) {
	rt := packageUpdateRuntime{platform: runtime.GOOS, current: current, uid: os.Geteuid()}
	var err error
	rt.home, err = os.UserHomeDir()
	if err != nil {
		return rt, errors.New("Cannot locate your home directory.")
	}
	rt.home, err = filepath.EvalSymlinks(rt.home)
	if err != nil {
		return rt, errors.New("Cannot resolve your home directory.")
	}
	rt.binary, err = os.Executable()
	if err == nil {
		rt.binary, err = filepath.EvalSymlinks(rt.binary)
	}
	if err != nil {
		return rt, errors.New("Cannot identify this installation.")
	}
	rt.capture = packageUpdateCapture(rt.home)
	return rt, nil
}

func packageUpdateAbsolute(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && len(path) <= 4096 && !strings.ContainsAny(path, "\x00\r\n")
}

func packageUpdateDigest(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<20 || info.Mode().Perm()&0022 != 0 {
		return "", errors.New("Cannot safely inspect the installed executable.")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", errors.New("Cannot open the installed executable.")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", errors.New("The installed executable changed while opening it.")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, io.LimitReader(f, (256<<20)+1)); err != nil {
		return "", errors.New("Cannot read the installed executable.")
	}
	after, err := f.Stat()
	if err != nil || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("The installed executable changed while reading it.")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func packageUpdateProbe(rt packageUpdateRuntime, name string, args ...string) (string, error) {
	parent := rt.context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	output, err := rt.capture(ctx, name, args...)
	if err != nil || len(output) > packageUpdateOutputLimit {
		return "", errors.New("Cannot verify this package-manager installation.")
	}
	return strings.TrimSpace(string(output)), nil
}

func packageUpdateBrewCandidates(home string) []string {
	paths := []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew", "/home/linuxbrew/.linuxbrew/bin/brew", filepath.Join(home, ".linuxbrew/bin/brew")}
	if path, err := exec.LookPath("brew"); err == nil && filepath.IsAbs(path) {
		paths = append(paths, path)
	}
	return paths
}

func (rt packageUpdateRuntime) digest(path string) (string, error) {
	if rt.fingerprint != nil {
		return rt.fingerprint(path)
	}
	return packageUpdateDigest(path)
}

// Ownership is established using the package database AND the executable's
// actual path. Merely having apt or brew installed never enables an update.
func detectPackageInstallation(rt packageUpdateRuntime) (packageInstallation, error) {
	manual := packageInstallation{Method: "manual", Binary: rt.binary, Relaunch: rt.binary, Version: rt.current}
	if rt.platform != "darwin" && rt.platform != "linux" {
		return manual, nil
	}
	if rt.uid == 0 {
		return manual, errors.New("Run Kilo Proxy as your ordinary user to update it.")
	}
	if !packageUpdateAbsolute(rt.binary) || !packageUpdateAbsolute(rt.home) || rt.capture == nil {
		return manual, errors.New("Cannot identify this installation safely.")
	}
	edition := "kilo-proxy-desktop"
	binaryName := "kilo-proxy"
	if filepath.Base(rt.binary) == "kilo-proxy-headless" {
		edition, binaryName = "kilo-proxy-headless", "kilo-proxy-headless"
	}
	if rt.platform == "linux" && rt.binary == "/usr/bin/"+binaryName {
		owner, err := packageUpdateProbe(rt, "/usr/bin/dpkg-query", "-S", rt.binary)
		if err != nil {
			return manual, err
		}
		parts := strings.Split(owner, ": ")
		if len(parts) != 2 || strings.Split(parts[0], ":")[0] != edition || parts[1] != rt.binary {
			return manual, nil
		}
		installed, err := packageUpdateProbe(rt, "/usr/bin/dpkg-query", "-W", "-f=${db:Status-Status}\t${Version}", edition)
		if err != nil || installed != "installed\t"+strings.Replace(rt.current, "-", "~", 1) {
			return manual, errors.New("APT's installed version does not match the running application. Reopen the updated app first.")
		}
		digest, err := rt.digest(rt.binary)
		return packageInstallation{Method: "apt", Manager: "/usr/bin/apt-get", Package: edition, Binary: rt.binary, Relaunch: rt.binary, Version: rt.current, Digest: digest}, err
	}
	for _, brew := range packageUpdateBrewCandidates(rt.home) {
		if _, err := os.Stat(brew); err != nil {
			continue
		}
		prefix, err := packageUpdateProbe(rt, brew, "--prefix")
		if err != nil || !packageUpdateAbsolute(prefix) {
			continue
		}
		canonicalBrew, err := filepath.EvalSymlinks(brew)
		if err != nil || !packageUpdateAbsolute(canonicalBrew) {
			continue
		}
		if rt.platform == "darwin" && edition == "kilo-proxy-desktop" {
			info, err := packageUpdateProbe(rt, brew, "info", "--json=v2", "--cask", "rosseca/tap/kilo-proxy")
			var data struct {
				Casks []struct {
					Token, Tap string
					Installed  *string
				}
			}
			if err != nil || json.Unmarshal([]byte(info), &data) != nil || len(data.Casks) != 1 || data.Casks[0].Token != "kilo-proxy" || data.Casks[0].Tap != "rosseca/tap" || data.Casks[0].Installed == nil || *data.Casks[0].Installed != rt.current {
				continue
			}
			listed, err := packageUpdateProbe(rt, brew, "list", "--cask", "rosseca/tap/kilo-proxy")
			if err != nil {
				continue
			}
			for _, line := range strings.Split(listed, "\n") {
				bundle := packageUpdateCaskPath(line)
				if !strings.HasSuffix(bundle, "/Kilo Proxy.app") {
					continue
				}
				actual, err := filepath.EvalSymlinks(filepath.Join(bundle, "Contents/MacOS/kilo-proxy"))
				if err != nil || actual != rt.binary {
					continue
				}
				digest, err := rt.digest(rt.binary)
				return packageInstallation{Method: "brew-cask", Manager: canonicalBrew, Package: "rosseca/tap/kilo-proxy", Binary: rt.binary, Relaunch: actual, Prefix: prefix, Version: rt.current, Digest: digest}, err
			}
			continue
		}
		cellar, err := packageUpdateProbe(rt, brew, "--cellar", "rosseca/tap/"+edition)
		if err != nil || !packageUpdateAbsolute(cellar) {
			continue
		}
		rel, err := filepath.Rel(cellar, rt.binary)
		if err != nil {
			continue
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) != 3 || parts[1] != "bin" || parts[2] != binaryName || (parts[0] != rt.current && !strings.HasPrefix(parts[0], rt.current+"_")) {
			continue
		}
		receipt, err := packageUpdateReadReceipt(filepath.Join(cellar, parts[0], "INSTALL_RECEIPT.json"))
		var tab struct {
			Source struct {
				Tap  string
				Spec string
			}
		}
		if err != nil || len(receipt) > packageUpdateOutputLimit || json.Unmarshal(receipt, &tab) != nil || tab.Source.Tap != "rosseca/tap" || tab.Source.Spec != "stable" {
			continue
		}
		installed, err := packageUpdateProbe(rt, brew, "list", "--versions", "rosseca/tap/"+edition)
		if err != nil || !strings.Contains(" "+installed+" ", " "+parts[0]+" ") {
			continue
		}
		digest, err := rt.digest(rt.binary)
		return packageInstallation{Method: "brew-formula", Manager: canonicalBrew, Package: "rosseca/tap/" + edition, Binary: rt.binary, Relaunch: filepath.Join(prefix, "opt", edition, "bin", binaryName), Prefix: prefix, Version: rt.current, Digest: digest}, err
	}
	return manual, nil
}

func packageUpdateCaskPath(line string) string {
	// Homebrew's Moved#summarize_installed appends " (files, size)".
	// Keep spaces/parentheses inside the app directory itself intact.
	if end := strings.LastIndex(line, "/Kilo Proxy.app ("); end >= 0 {
		line = line[:end+len("/Kilo Proxy.app")]
	}
	return strings.TrimSuffix(line, "/")
}

func packageUpdateReadReceipt(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > packageUpdateOutputLimit || info.Mode().Perm()&0022 != 0 {
		return nil, errors.New("Invalid package receipt.")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("Package receipt changed.")
	}
	data, err := io.ReadAll(io.LimitReader(f, packageUpdateOutputLimit+1))
	if len(data) > packageUpdateOutputLimit {
		return nil, errors.New("Package receipt exceeded its size limit.")
	}
	return data, err
}

func packageUpdateCandidateBrew(output []byte, cask bool) string {
	var data struct {
		Formulae []struct {
			FullName string `json:"full_name"`
			Versions struct{ Stable string }
		}
		Casks []struct{ Token, Tap, Version string }
	}
	if len(output) > packageUpdateOutputLimit || json.Unmarshal(output, &data) != nil {
		return ""
	}
	if cask {
		if len(data.Casks) == 1 && data.Casks[0].Tap == "rosseca/tap" && data.Casks[0].Token == "kilo-proxy" {
			return data.Casks[0].Version
		}
	} else if len(data.Formulae) == 1 && (data.Formulae[0].FullName == "rosseca/tap/kilo-proxy-desktop" || data.Formulae[0].FullName == "rosseca/tap/kilo-proxy-headless") {
		return data.Formulae[0].Versions.Stable
	}
	return ""
}

func packageUpdateAtLeast(value, target string) bool {
	v, valid := parseReleaseVersion(value)
	t, targetValid := parseReleaseVersion(target)
	return valid && targetValid && v.pre == "" && t.pre == "" && compareReleaseVersions(v, t) >= 0
}

// The candidate must exist in the package feed after its catalog is refreshed.
// The GitHub release alone is insufficient: tap/APT publication can lag behind.
func executePackageUpgrade(ctx context.Context, installation packageInstallation, target string, env []string, stdout, stderr io.Writer) (string, error) {
	return executePackageUpgradeWith(ctx, installation, target, env, stdout, stderr, func(ctx context.Context, command string, args []string, capture bool) ([]byte, error) {
		cmd := exec.CommandContext(ctx, command, args...)
		cmd.WaitDelay = 2 * time.Second
		cmd.Env = env
		cmd.Stdin = os.Stdin
		if capture {
			var b packageUpdateBuffer
			cmd.Stdout, cmd.Stderr = &b, io.Discard
			err := cmd.Run()
			if b.overflow {
				return nil, errors.New("Package response too large.")
			}
			return b.data, err
		}
		cmd.Stdout, cmd.Stderr = stdout, stderr
		return nil, cmd.Run()
	})
}

type packageUpdateCommand func(context.Context, string, []string, bool) ([]byte, error)

func executePackageUpgradeWith(ctx context.Context, in packageInstallation, target string, env []string, stdout, stderr io.Writer, run packageUpdateCommand) (string, error) {
	if !packageUpdateAtLeast(target, target) || !packageUpdateAbsolute(in.Manager) || !packageUpdateAbsolute(in.Relaunch) {
		return "", errors.New("Invalid update plan.")
	}
	runVisible := func(command string, args ...string) error { _, err := run(ctx, command, args, false); return err }
	probe := func(command string, args ...string) ([]byte, error) {
		probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return run(probeCtx, command, args, true)
	}
	switch in.Method {
	case "apt":
		if in.Manager != "/usr/bin/apt-get" || (in.Package != "kilo-proxy-desktop" && in.Package != "kilo-proxy-headless") || in.Relaunch != in.Binary || !strings.HasPrefix(in.Binary, "/usr/bin/") {
			return "", errors.New("Invalid APT update plan.")
		}
		if err := runVisible("/usr/bin/sudo", in.Manager, "update"); err != nil {
			return "", errors.New("APT could not refresh its package catalog. No application was restarted.")
		}
		policy, err := probe("/usr/bin/apt-cache", "policy", in.Package)
		if err != nil || !packageUpdateAPTCandidate(string(policy), target) {
			return "", errors.New("The new version is not available from the Kilo Proxy APT repository yet. Try again later.")
		}
		if err = runVisible("/usr/bin/sudo", in.Manager, "install", "--only-upgrade", in.Package+"="+target); err != nil {
			return "", errors.New("APT did not complete the update. Check the terminal output before reopening Kilo Proxy.")
		}
	case "brew-cask", "brew-formula":
		if in.Method == "brew-cask" && in.Package != "rosseca/tap/kilo-proxy" || in.Method == "brew-formula" && in.Package != "rosseca/tap/kilo-proxy-desktop" && in.Package != "rosseca/tap/kilo-proxy-headless" {
			return "", errors.New("Invalid Homebrew update plan.")
		}
		if err := runVisible(in.Manager, "update"); err != nil {
			return "", errors.New("Homebrew could not refresh its package catalog. No application was restarted.")
		}
		args := []string{"info", "--json=v2"}
		if in.Method == "brew-cask" {
			args = append(args, "--cask")
		}
		args = append(args, in.Package)
		info, err := probe(in.Manager, args...)
		if err != nil || !packageUpdateAtLeast(packageUpdateCandidateBrew(info, in.Method == "brew-cask"), target) {
			return "", errors.New("The new version is not available in the Kilo Proxy Homebrew tap yet. Try again later.")
		}
		args = []string{"upgrade"}
		if in.Method == "brew-cask" {
			args = append(args, "--cask")
		}
		args = append(args, in.Package)
		if err = runVisible(in.Manager, args...); err != nil {
			return "", errors.New("Homebrew did not complete the update. Check the terminal output before reopening Kilo Proxy.")
		}
	default:
		return "", errors.New("This installation must be updated from the download page.")
	}
	output, err := probe(in.Relaunch, "--version")
	if err != nil || !packageUpdateAtLeast(strings.TrimSpace(string(output)), target) {
		return "", errors.New("The installed program did not report the requested new version. It has not been restarted.")
	}
	fmt.Fprintln(stdout, "Kilo Proxy", strings.TrimSpace(string(output)), "installed.")
	return in.Relaunch, nil
}

func packageUpdateAPTCandidate(policy, target string) bool {
	if len(policy) > packageUpdateOutputLimit {
		return false
	}
	// Require an exact version entry sourced from our public signed repository;
	// do not accidentally select a similarly named package from another archive.
	match, official, foreign := false, false, false
	for _, line := range strings.Split(policy, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "***" {
			fields = fields[1:]
		}
		if len(fields) == 2 {
			if _, valid := parseReleaseVersion(fields[0]); valid {
				if match {
					return official && !foreign
				}
				match = fields[0] == target
				continue
			}
		}
		if match && len(fields) >= 2 && releaseNumericIdentifier(fields[0]) {
			if strings.TrimSuffix(fields[1], "/") == "https://rosseca.github.io/kilo-proxy/apt" {
				official = true
			} else if fields[1] != "/var/lib/dpkg/status" {
				foreign = true
			}
		}
	}
	return match && official && !foreign
}

// Refresh ONLY already-installed wrappers whose complete bytes identify this
// executable and profile. Never create commands, edit shell startup files, or
// switch wrappers that currently belong to another profile.
func refreshPackageUpdateWrappers(home, profile, oldBinary, newBinary string) error {
	var changes []terminalInstallFile
	for _, pair := range [][2]string{{"kilo-codex", "codex-cli"}, {"kilo-claude", "claude"}, {"kilo-omp", "omp"}, {"kilo-opencode", "opencode"}} {
		path := filepath.Join(home, ".local/bin", pair[0])
		file, err := readTerminalInstallFile(home, path)
		if err != nil {
			return errors.New("Cannot inspect the installed terminal commands safely. Refresh them from Settings after reopening.")
		}
		if !file.exists || !bytes.Equal(file.old, terminalCommandScript(oldBinary, profile, pair[1])) {
			continue
		}
		file.data = terminalCommandScript(newBinary, profile, pair[1])
		changes = append(changes, file)
	}
	for _, file := range changes {
		current, err := readTerminalInstallFile(home, file.path)
		if err != nil || !current.exists || !bytes.Equal(current.old, file.old) {
			return errors.New("A terminal command changed during the update. Refresh it from Settings after reopening.")
		}
		if err = writeTerminalInstallFile(file.path, file.data, file.oldMode); err != nil {
			return errors.New("Could not refresh terminal commands. Reopen Kilo Proxy and install them again from Settings.")
		}
	}
	return nil
}
