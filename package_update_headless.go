package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Headless updates run in the caller's terminal. They never stop a server,
// modify a user service or open another process with the user's profile.
type headlessUpdateDependencies struct {
	headless bool
	runtime  func() (packageUpdateRuntime, error)
	detect   func(packageUpdateRuntime) (packageInstallation, error)
	latest   func(context.Context) (string, error)
	upgrade  func(context.Context, packageInstallation, string, []string, io.Writer, io.Writer) (string, error)
	verify   func(packageUpdateRuntime, packageInstallation, string, string) error
	wrappers func(string, string, string, string) error
}

func runHeadlessUpdateCLI(dir string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	checker := newReleaseUpdateChecker(version)
	defer checker.close()
	deps := headlessUpdateDependencies{
		headless: headlessBinary,
		runtime:  func() (packageUpdateRuntime, error) { return newPackageUpdateRuntime(version) },
		detect:   detectPackageInstallation,
		latest: func(ctx context.Context) (string, error) {
			tag, _, message := checker.fetchLatest(ctx)
			if message != "" {
				return "", errors.New(message)
			}
			return strings.TrimPrefix(tag, "v"), nil
		},
		upgrade:  executePackageUpgrade,
		verify:   verifyPackageUpdateOwnership,
		wrappers: refreshPackageUpdateWrappers,
	}
	return runHeadlessUpdateWith(dir, args, stdin, stdout, stderr, deps)
}

func headlessUpdateServiceAbsent(dir string) error {
	_, err := os.Lstat(filepath.Join(dir, headlessServiceManifestFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("Cannot inspect this profile's user service safely.")
	}
	return errors.New("This profile has an installed user service. Run stop, then service uninstall with this same --config-dir before update install. After updating, run service install with the same --config-dir to restore it; your credentials and chats are preserved.")
}

func runHeadlessUpdateWith(dir string, args []string, stdin io.Reader, stdout, stderr io.Writer, deps headlessUpdateDependencies) int {
	action, yes := "check", false
	if len(args) > 0 {
		action, args = args[0], args[1:]
	}
	if action == "--help" || action == "-h" {
		fmt.Fprintln(stdout, "Usage: update [check|install [--yes]]. Stop this profile before installing. APT may ask for your password in this terminal.")
		return 0
	}
	if action == "install" && len(args) == 1 && args[0] == "--yes" {
		yes, args = true, nil
	}
	if (action != "check" && action != "install") || len(args) != 0 {
		fmt.Fprintln(stderr, "Usage: update [check|install [--yes]]")
		return 2
	}
	rt, err := deps.runtime()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !deps.headless && filepath.Base(rt.binary) != "kilo-proxy-headless" {
		fmt.Fprintln(stderr, "Use Settings > App updates in the desktop app. This command updates kilo-proxy-headless.")
		return 1
	}
	installation, err := deps.detect(rt)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), releaseUpdateTimeout)
	latest, err := deps.latest(ctx)
	cancel()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	currentVersion, currentOK := parseReleaseVersion(rt.current)
	latestVersion, latestOK := parseReleaseVersion(latest)
	if !currentOK || !latestOK || latestVersion.pre != "" {
		fmt.Fprintln(stderr, "Cannot verify the current and latest stable versions.")
		return 1
	}
	fmt.Fprintf(stdout, "Kilo Proxy headless %s; latest stable %s. Installation: %s.\n", rt.current, latest, installation.Method)
	if compareReleaseVersions(latestVersion, currentVersion) <= 0 {
		fmt.Fprintln(stdout, "You are up to date.")
		return 0
	}
	if installation.Method == "manual" {
		fmt.Fprintln(stdout, "Download the new headless package from "+releaseUpdatePagePrefix+"v"+latest)
		if action == "install" {
			return 1
		}
		return 0
	}
	if action == "check" {
		fmt.Fprintln(stdout, "An update is available. Run update install with this same --config-dir after stopping the profile.")
		return 0
	}
	if err = headlessUpdateServiceAbsent(dir); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !yes {
		fmt.Fprintf(stdout, "Update %s to %s using its package manager? [y/N] ", installation.Package, latest)
		scanner := bufio.NewScanner(io.LimitReader(stdin, 128))
		if !scanner.Scan() || (strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" && strings.ToLower(strings.TrimSpace(scanner.Text())) != "yes") {
			fmt.Fprintln(stdout, "Update cancelled.")
			return 0
		}
	}
	unlock, err := acquireProfileLock(dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer unlock()
	if err = headlessUpdateServiceAbsent(dir); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// Revalidate after confirmation and lock acquisition: the binary/package
	// can have changed while the user was deciding whether to update.
	currentInstallation, err := deps.detect(rt)
	if err != nil || currentInstallation != installation {
		fmt.Fprintln(stderr, "This installation changed. Run update check again before installing.")
		return 1
	}
	upgradeCtx, finish := context.WithTimeout(context.Background(), 90*time.Minute)
	defer finish()
	newBinary, err := deps.upgrade(upgradeCtx, installation, latest, packageUpdateEnvironment(rt.home), stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err = deps.verify(rt, installation, newBinary, latest); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err = deps.wrappers(rt.home, dir, rt.binary, newBinary); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "Update complete. Start serve again with this same --config-dir when ready.")
	return 0
}
