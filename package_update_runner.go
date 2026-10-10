package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type packageUpdatePlan struct {
	Version                                int
	Expires                                int64
	Installation                           packageInstallation
	Profile, Home, Platform, Target, Token string
	Args, Environment, RestartEnvironment  []string
}

type packageUpdateResult struct {
	Version int    `json:"version"`
	Failed  bool   `json:"failed"`
	Target  string `json:"target"`
}

type packageUpdateManager struct {
	mu                  sync.Mutex
	rt                  packageUpdateRuntime
	profile             string
	args                []string
	installation        packageInstallation
	checked, installing bool
	message             string
	terminal            func(string, string, string, []string) error
	quit                func()
	done                <-chan struct{}
	readyTimeout        time.Duration // Synthetic terminal tests may shorten this.
}

func (a *app) configurePackageUpdates(args []string) {
	rt, err := newPackageUpdateRuntime(version)
	if err != nil {
		return
	}
	m := &packageUpdateManager{rt: rt, profile: a.dir, args: args, terminal: startPackageUpdateTerminal, quit: a.requestQuit, done: a.quit, message: "detecting_installation"}
	a.packageUpdates = m
	go func() {
		installation, err := detectPackageInstallation(rt)
		message := "managed_installation"
		if installation.Method == "manual" {
			message = "manual_installation"
		}
		if err != nil {
			message = "package_manager_unavailable"
		}
		if rt.uid == 0 {
			message = "run_as_user"
		}
		if data, err := headlessServiceReadPrivate(filepath.Join(a.dir, packageUpdateResultFile)); err == nil {
			var result packageUpdateResult
			if json.Unmarshal(data, &result) == nil && result.Version == 1 && result.Failed {
				message = "package_update_failed"
			}
		}
		m.mu.Lock()
		m.installation, m.checked, m.message = installation, true, message
		m.mu.Unlock()
	}()
}

func (m *packageUpdateManager) snapshot(state *releaseUpdateState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state.InstallMethod = m.installation.Method
	if state.InstallMethod == "" {
		state.InstallMethod = "manual"
	}
	state.CanInstall = m.checked && m.installation.Method != "manual" && m.installation.Digest != "" && m.rt.uid != 0
	state.InstallMessage, state.Installing = m.message, m.installing
}

func (a *app) installPackageUpdateAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		jsonError(w, 405, "Method not allowed.")
		return
	}
	var request struct {
		Version string `json:"version"`
		Confirm bool   `json:"confirm"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	if !request.Confirm {
		jsonError(w, 409, "Confirm the update before continuing.")
		return
	}
	state := a.updateSnapshot()
	if !state.Available || !validReleaseUpdateURL(state.ReleaseURL) || state.LatestVersion != request.Version || !packageUpdateAtLeast(request.Version, request.Version) {
		jsonError(w, 409, "Check the latest stable version again before updating.")
		return
	}
	if a.headless || a.packageUpdates == nil || !state.CanInstall {
		jsonError(w, 409, "This installation must be updated using its package manager or the download page.")
		return
	}
	if !a.launchMu.TryLock() {
		jsonError(w, 409, "Another launch or installation is being prepared.")
		return
	}
	defer a.launchMu.Unlock()
	a.mu.Lock()
	active := a.active + a.imageUploadsActive
	a.mu.Unlock()
	a.imageGenerationMu.Lock()
	active += a.imageGenerationActive
	a.imageGenerationMu.Unlock()
	if active != 0 {
		jsonError(w, 409, "Wait for active requests and uploads to finish before updating.")
		return
	}
	if err := a.packageUpdates.startWithContext(r.Context(), request.Version); err != nil {
		jsonError(w, 409, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]any{"started": true, "message": "The update terminal is opening. Kilo Proxy will close only after the updater is ready."})
}

func (m *packageUpdateManager) fail(message string) {
	m.mu.Lock()
	m.installing = false
	m.message = message
	m.mu.Unlock()
}

func (m *packageUpdateManager) start(target string) error {
	return m.startWithContext(context.Background(), target)
}

func (m *packageUpdateManager) startWithContext(ctx context.Context, target string) error {
	if err := ctx.Err(); err != nil {
		return errors.New("The update request was cancelled. The application is still running.")
	}
	m.mu.Lock()
	if m.installing {
		m.mu.Unlock()
		return errors.New("An update is already being prepared.")
	}
	if !m.checked || m.installation.Method == "manual" || m.installation.Digest == "" {
		m.mu.Unlock()
		return errors.New("This installation cannot be updated automatically.")
	}
	old := m.installation
	m.installing = true
	m.message = "update_starting"
	m.mu.Unlock()
	rt := m.rt
	rt.context = ctx
	current, err := detectPackageInstallation(rt)
	if ctx.Err() != nil {
		m.fail("update_start_failed")
		return errors.New("The update request was cancelled. The application is still running.")
	}
	if err != nil || current != old {
		m.fail("installation_changed")
		return errors.New("The installation changed. Reopen Kilo Proxy before updating.")
	}
	restartEnvironment, err := packageUpdateRestartEnvironment(m.rt.home)
	if err != nil {
		m.fail("update_start_failed")
		return err
	}
	plan := packageUpdatePlan{Version: 1, Expires: time.Now().Add(5 * time.Minute).Unix(), Installation: current, Profile: m.profile, Home: m.rt.home, Platform: m.rt.platform, Target: target, Token: randomKey(""), Args: m.args, Environment: packageUpdateEnvironment(m.rt.home), RestartEnvironment: restartEnvironment}
	root, helper, ticket, err := writePackageUpdatePlan(plan)
	if err != nil {
		m.fail("update_start_failed")
		return errors.New("Cannot prepare the private update helper. The application is still running.")
	}
	if ctx.Err() != nil {
		_ = os.RemoveAll(root)
		m.fail("update_start_failed")
		return errors.New("The update request was cancelled. The application is still running.")
	}
	script := filepath.Join(root, "update.command")
	if err = m.terminal(helper, ticket, script, plan.Environment); err != nil {
		_ = os.RemoveAll(root)
		m.fail("update_start_failed")
		return errors.New("Cannot open an update terminal. The application is still running.")
	}
	go func() {
		budget := m.readyTimeout
		if budget <= 0 {
			// Homebrew ownership uses several bounded probes before acknowledging.
			budget = 90 * time.Second
		}
		deadline := time.NewTimer(budget)
		defer deadline.Stop()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-m.done:
				_ = os.RemoveAll(root)
				return
			case <-deadline.C:
				_ = os.RemoveAll(root)
				m.fail("update_start_failed")
				return
			case <-ticker.C:
				data, readErr := headlessServiceReadPrivate(filepath.Join(root, "ready"))
				if readErr != nil || string(data) != plan.Token {
					continue
				}
				if err = os.WriteFile(filepath.Join(root, "proceed"), []byte(plan.Token), 0600); err != nil {
					_ = os.RemoveAll(root)
					m.fail("update_start_failed")
					return
				}
				m.quit()
				return
			}
		}
	}()
	return nil
}

func validatePackageUpdatePlan(plan packageUpdatePlan) error {
	if plan.Version != 1 || plan.Expires < time.Now().Unix() || plan.Expires > time.Now().Add(6*time.Minute).Unix() || len(plan.Token) != 64 || plan.Platform != runtime.GOOS || plan.Platform != "darwin" && plan.Platform != "linux" || !packageUpdateAtLeast(plan.Target, plan.Target) {
		return errors.New("The update plan is invalid or expired.")
	}
	for _, path := range []string{plan.Profile, plan.Home, plan.Installation.Binary, plan.Installation.Relaunch, plan.Installation.Manager} {
		if !packageUpdateAbsolute(path) {
			return errors.New("Invalid update path.")
		}
	}
	if len(plan.Installation.Digest) != 64 || plan.Installation.Version == "" || len(plan.Args) > 3 || len(plan.Environment) > 30 || len(plan.RestartEnvironment) > 30 {
		return errors.New("Invalid update identity.")
	}
	for _, arg := range plan.Args {
		if arg != "--no-browser" && arg != "--no-tray" && arg != "--browser" {
			return errors.New("Invalid restart option.")
		}
	}
	for index, environment := range [][]string{plan.Environment, plan.RestartEnvironment} {
		allowed := map[string]bool{}
		template := packageUpdateEnvironment(plan.Home)
		if index == 1 {
			template = appendPackageUpdateSessionEnvironment([]string{"HOME=" + plan.Home, "PATH=" + packageUpdateManagerPath})
		}
		for _, entry := range template {
			name, _, _ := strings.Cut(entry, "=")
			allowed[name] = true
		}
		seen := map[string]bool{}
		for _, entry := range environment {
			name, value, ok := strings.Cut(entry, "=")
			limit := 8192
			if index == 1 && name == "PATH" {
				limit = 32768
			}
			if !ok || !allowed[name] || seen[name] || len(value) > limit || strings.ContainsAny(value, "\x00\r\n") || name == "HOME" && value != plan.Home || name == "PATH" && (index == 0 && value != packageUpdateManagerPath || index == 1 && !validPackageUpdateRestartPath(value)) {
				return errors.New("Invalid update environment.")
			}
			seen[name] = true
		}
		if !seen["HOME"] || !seen["PATH"] {
			return errors.New("Incomplete update environment.")
		}
	}
	return nil
}

func writePackageUpdatePlan(plan packageUpdatePlan) (root, helper, ticket string, err error) {
	if err = validatePackageUpdatePlan(plan); err != nil {
		return
	}
	root, err = os.MkdirTemp("", "kilo-package-update-")
	if err != nil {
		return
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(root)
		}
	}()
	helper, ticket = filepath.Join(root, "runner"), filepath.Join(root, "plan.json")
	source, openErr := os.Open(plan.Installation.Binary)
	if openErr != nil {
		err = openErr
		return
	}
	defer source.Close()
	destination, createErr := os.OpenFile(helper, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if createErr != nil {
		err = createErr
		return
	}
	_, err = io.Copy(destination, io.LimitReader(source, (256<<20)+1))
	closeErr := destination.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return
	}
	digest, digestErr := packageUpdateDigest(helper)
	if digestErr != nil || digest != plan.Installation.Digest {
		err = errors.New("The executable changed while preparing its helper.")
		return
	}
	data, jsonErr := json.Marshal(plan)
	if jsonErr != nil {
		err = jsonErr
		return
	}
	if err = os.WriteFile(ticket, data, 0600); err != nil {
		return
	}
	command := "#!/bin/sh\n/usr/bin/env -i"
	for _, entry := range plan.Environment {
		command += " " + helperShellQuote(entry)
	}
	command += " " + helperShellQuote(helper) + " " + packageUpdateRunnerFlag + " " + helperShellQuote(ticket) + "\nstatus=$?\nif [ \"$status\" -ne 0 ]; then printf '\\nPress Return to close this update window.\\n'; IFS= read -r response; fi\nexit \"$status\"\n"
	err = os.WriteFile(filepath.Join(root, "update.command"), []byte(command), 0700)
	return
}

func startPackageUpdateTerminal(helper, ticket, script string, env []string) error {
	invocation, err := clientTerminalCommandFor(runtime.GOOS, helper, ticket, script, exec.LookPath)
	if err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		// Keep errors visible even if the desktop terminal would normally close
		// as soon as its child exits. The bootstrap waits for Return on failure.
		invocation.Args = append(invocation.Args[:len(invocation.Args)-3], "/bin/sh", script)
	}
	command := exec.Command(invocation.Executable, invocation.Args...)
	command.Env = env
	if err = command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}

func readPackageUpdatePlan(path string) (packageUpdatePlan, error) {
	var plan packageUpdatePlan
	root := filepath.Dir(path)
	if !packageUpdateAbsolute(path) || filepath.Base(path) != "plan.json" || !strings.HasPrefix(filepath.Base(root), "kilo-package-update-") {
		return plan, errors.New("Invalid update ticket.")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || !headlessFileOwned(info) {
		return plan, errors.New("The update directory is not private.")
	}
	// Rename before reading: only one helper can consume this ticket.
	consumed := filepath.Join(root, "consumed.json")
	if _, err = os.Lstat(consumed); !errors.Is(err, os.ErrNotExist) {
		return plan, errors.New("The update ticket was already consumed.")
	}
	if err = os.Rename(path, consumed); err != nil {
		return plan, errors.New("The update ticket was already consumed.")
	}
	data, err := headlessServiceReadPrivate(consumed)
	if err != nil || json.Unmarshal(data, &plan) != nil {
		return plan, errors.New("Cannot read the private update plan.")
	}
	if err = validatePackageUpdatePlan(plan); err != nil {
		return plan, err
	}
	for _, directory := range []string{plan.Home, plan.Profile} {
		info, err = os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || !headlessFileOwned(info) {
			return plan, errors.New("The update profile and home must be owned by you and not writable by others.")
		}
	}
	helper, err := os.Executable()
	if err != nil || helper != filepath.Join(root, "runner") {
		return plan, errors.New("Invalid update helper identity.")
	}
	digest, err := packageUpdateDigest(helper)
	if err != nil || digest != plan.Installation.Digest {
		return plan, errors.New("The update helper changed.")
	}
	return plan, nil
}

func packageUpdateWaitMarker(root, name, token string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := headlessServiceReadPrivate(filepath.Join(root, name))
		if err == nil && string(data) == token {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("The running application did not authorize this update. Nothing was installed.")
}

func packageUpdateWaitProfile(profile string, timeout time.Duration) (func(), error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		release, err := acquireProfileLock(profile)
		if err == nil {
			return release, nil
		}
		if !errors.Is(err, errProfileLocked) {
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, errors.New("Kilo Proxy did not finish closing. Nothing was installed.")
}

func runPackageUpdateMode(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "A private update ticket is required.")
		return 1
	}
	plan, err := readPackageUpdatePlan(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	root := filepath.Dir(args[0])
	defer os.RemoveAll(root)
	fail := func(err error) int {
		data, _ := json.Marshal(packageUpdateResult{Version: 1, Failed: true, Target: plan.Target})
		_ = writeTerminalInstallFile(filepath.Join(plan.Profile, packageUpdateResultFile), data, 0600)
		fmt.Fprintln(os.Stderr, "Update stopped:", err, "\nKilo Proxy was not restarted. Your profile and chats are preserved.")
		return 1
	}
	rt := packageUpdateRuntime{platform: plan.Platform, home: plan.Home, binary: plan.Installation.Binary, current: plan.Installation.Version, uid: os.Geteuid(), capture: packageUpdateCapture(plan.Home)}
	current, err := detectPackageInstallation(rt)
	if err != nil || current != plan.Installation {
		return fail(errors.New("The installed program or package ownership changed. Nothing was installed."))
	}
	if err = os.WriteFile(filepath.Join(root, "ready"), []byte(plan.Token), 0600); err != nil {
		return fail(errors.New("Cannot signal that the update helper is ready."))
	}
	if err = packageUpdateWaitMarker(root, "proceed", plan.Token, 30*time.Second); err != nil {
		return fail(err)
	}
	release, err := packageUpdateWaitProfile(plan.Profile, 120*time.Second)
	if err != nil {
		return fail(err)
	}
	defer release()
	current, err = detectPackageInstallation(rt)
	if err != nil || current != plan.Installation {
		return fail(errors.New("The installation changed before the update. Nothing was installed."))
	}
	fmt.Fprintln(os.Stdout, "Updating Kilo Proxy. Keep this terminal open. Only the selected Kilo Proxy package is upgraded.")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	newBinary, err := executePackageUpgrade(ctx, current, plan.Target, plan.Environment, os.Stdout, os.Stderr)
	if err != nil {
		return fail(err)
	}
	// Verify package ownership again against the newly installed version and
	// stable launch path, including cask relocation and formula opt symlinks.
	realBinary, err := filepath.EvalSymlinks(newBinary)
	if err != nil {
		return fail(errors.New("Cannot locate the newly installed executable."))
	}
	installedVersion, err := packageUpdateProbe(rt, newBinary, "--version")
	if err != nil {
		return fail(errors.New("Cannot verify the newly installed version."))
	}
	rt.binary, rt.current = realBinary, installedVersion
	updated, err := detectPackageInstallation(rt)
	if err != nil || updated.Method != current.Method || updated.Package != current.Package || updated.Manager != current.Manager || !packageUpdateAtLeast(updated.Version, plan.Target) {
		return fail(errors.New("Cannot verify ownership of the newly installed application."))
	}
	if err = refreshPackageUpdateWrappers(plan.Home, plan.Profile, current.Binary, newBinary); err != nil {
		return fail(err)
	}
	data, _ := json.Marshal(packageUpdateResult{Version: 1, Target: plan.Target})
	if err = writeTerminalInstallFile(filepath.Join(plan.Profile, packageUpdateResultFile), data, 0600); err != nil {
		return fail(errors.New("Cannot save the completed update status."))
	}
	release()
	restartArgs := append([]string{"--config-dir", plan.Profile}, plan.Args...)
	command := exec.Command(newBinary, restartArgs...)
	command.Env = plan.RestartEnvironment
	// Do not let a relaunched desktop retain the update terminal's pipes.
	if err = command.Start(); err != nil {
		return fail(errors.New("The package was installed but Kilo Proxy could not be started. Open it from the applications menu."))
	}
	go func() { _ = command.Wait() }()
	fmt.Fprintln(os.Stdout, "Started Kilo Proxy with your existing profile.")
	return 0
}
