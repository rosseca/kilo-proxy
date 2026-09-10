//go:build desktop

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"

	"fyne.io/systray"
)

func (a *app) desktopTestGateway() func() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"test/native","name":"Native test model","context_length":64000,"pricing":{"prompt":"0.000001","completion":"0.000002"}}]}`))
		} else {
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	a.upstream, _ = url.Parse(server.URL)
	a.accountURL = server.URL
	return server.Close
}

// Only an explicit command-line flag enables this controller. All actions use a
// fresh profile and a synthetic local gateway; there is no remote test endpoint.
func (d *nativeDesktop) selfTest(reportPath string) {
	report := struct {
		Version string   `json:"version"`
		OS      string   `json:"os"`
		Arch    string   `json:"arch"`
		Checks  []string `json:"checks"`
		Passed  bool     `json:"passed"`
		Error   string   `json:"error,omitempty"`
	}{Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH}
	err := d.checkDesktop(&report.Checks)
	report.Passed = err == nil
	if err != nil {
		report.Error = err.Error()
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	if writeErr := os.WriteFile(reportPath, data, 0600); writeErr != nil {
		fmt.Fprintln(os.Stderr, "Could not write desktop check report:", writeErr)
		err = writeErr
	}
	if err != nil {
		d.mu.Lock()
		d.exitCode = 1
		d.mu.Unlock()
	}
	d.dispatch("quit")
}

func desktopWait(description string, check func() bool) error {
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", description)
}

// UI operations execute inside a real native FrameEvent, never from the testing
// goroutine. This exercises the same controls and state as normal interaction.
func (d *nativeDesktop) withUI(fn func() error) error {
	result := make(chan error, 1)
	d.mu.Lock()
	d.jobs = append(d.jobs, func() { result <- fn() })
	w := d.window
	d.mu.Unlock()
	if w == nil {
		return errors.New("native window is closed")
	}
	w.Invalidate()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		return errors.New("native frame did not process the UI action")
	case <-d.owner.quit:
		return errors.New("application quit before the desktop check completed")
	}
}

func (d *nativeDesktop) snapshot() (map[string]string, error) {
	var value map[string]string
	err := d.withUI(func() error { value = d.ui.SmokeSnapshot(); return nil })
	return value, err
}

func (d *nativeDesktop) readClipboard() (string, error) {
	result := make(chan string, 1)
	d.mu.Lock()
	d.clipboardRead = result
	w := d.window
	d.mu.Unlock()
	if w == nil {
		return "", errors.New("cannot read clipboard without a native window")
	}
	w.Invalidate()
	select {
	case text := <-result:
		return text, nil
	case <-time.After(3 * time.Second):
		return "", errors.New("native clipboard did not answer")
	}
}

func (d *nativeDesktop) sharedModelSmokeSaved(name string) bool {
	state := d.owner.modelLibrary.snapshot()
	data, err := readModelLibraryFile(filepath.Join(d.owner.dir, "models.json"))
	if err != nil {
		return false
	}
	disk, err := decodeModelLibrary(data)
	if err != nil || !reflect.DeepEqual(disk, state.Library) || len(disk.Models) != 2 || disk.DefaultModel != modelSmokeDefault {
		return false
	}
	choice := disk.Models[1]
	return disk.Models[0].ID == modelSmokeFirst && choice.ID == modelSmokeDefault && choice.DisplayName == name && choice.ReasoningCustom && choice.ReasoningEffort == "high" && reflect.DeepEqual(choice.ReasoningLevels, []string{"low", "high"})
}

func (d *nativeDesktop) checkDesktop(checks *[]string) error {
	passed := func(name string) {
		*checks = append(*checks, name)
		fmt.Fprintln(os.Stderr, "Native check passed:", name)
	}
	if err := desktopWait("the rendered native UI and authenticated backend", func() bool {
		state, err := d.snapshot()
		d.mu.Lock()
		frames := d.frames
		d.mu.Unlock()
		return err == nil && frames > 0 && state["content-ready"] == "true" && state["version"] == version
	}); err != nil {
		return err
	}
	passed("rendered-ui-and-authenticated-backend")
	if err := d.withUI(func() error {
		if d.ui.page != "setup" || d.ui.setupStep != setupConnect || d.ui.buttons["connection.login"] == nil {
			return errors.New("fresh application did not render the first-run sign-in guide")
		}
		return nil
	}); err != nil {
		return err
	}
	passed("first-run-sign-in-guide")
	for _, language := range []string{"en", "es"} {
		if err := d.withUI(func() error { return d.ui.SmokeAction("set-language", language) }); err != nil {
			return err
		}
		progress := "awaiting the first language state"
		if err := desktopWait("native window and tray language "+language, func() bool {
			state, err := d.snapshot()
			d.owner.mu.Lock()
			saved := d.owner.config.Language
			d.owner.mu.Unlock()
			d.mu.Lock()
			label := d.quitLabel
			d.mu.Unlock()
			progress = fmt.Sprintf("UI=%q saved=%q tray=%q pending=%q frame-error=%v", state["language"], saved, label, state["language-saving"], err)
			// Selecting the existing default still submits a real language POST.
			// Await its completion before submitting the next choice, otherwise
			// an unchanged English value can hide an in-flight save.
			return err == nil && state["language-saving"] == "false" && state["language"] == language && saved == language && label == trayText(language).exit
		}); err != nil {
			return fmt.Errorf("%w (%s)", err, progress)
		}
		passed("window-and-tray-language-" + language)
	}
	for step, mode := range []string{trayDisplayIcon, trayDisplaySpend, trayDisplayIcon} {
		if err := d.withUI(func() error {
			d.ui.saveTraySettings(mode)
			return nil
		}); err != nil {
			return err
		}
		if err := desktopWait("persisted tray appearance "+mode, func() bool {
			ready := false
			err := d.withUI(func() error {
				ready = !d.ui.busy["PUT/api/tray-settings"] && d.ui.value("appearance.tray") == mode
				return nil
			})
			if err != nil || !ready {
				return false
			}
			saved, err := readSettings(d.owner.dir)
			if err != nil || saved.TrayDisplay != mode {
				return false
			}
			if title, icon, supported := systray.TrayAppearance(); supported {
				if mode == trayDisplaySpend {
					return title == "$0.00" && icon
				}
				return title == "" && icon
			}
			return true
		}); err != nil {
			return err
		}
		checkMode := mode
		if step == 2 {
			checkMode = "icon-restored"
		}
		passed("tray-appearance-persisted-" + checkMode)
		if runtime.GOOS == "darwin" {
			passed("cocoa-tray-title-and-image-" + checkMode)
		}
	}
	if err := d.withUI(func() error { return d.ui.SmokeAction("seed-shared-models", "") }); err != nil {
		return err
	}
	if err := desktopWait("shared models, name, default and reasoning on disk", func() bool {
		state, err := d.snapshot()
		return err == nil && state["shared-model-saving"] == "false" && d.sharedModelSmokeSaved(modelSmokeName)
	}); err != nil {
		return err
	}
	passed("shared-model-library-autosave")
	for _, page := range []string{"agents", "models", "activity", "settings"} {
		if err := d.withUI(func() error { return d.ui.SmokeAction("navigate-primary", page) }); err != nil {
			return err
		}
		if err := desktopWait("primary navigation to "+page, func() bool {
			state, err := d.snapshot()
			return err == nil && state["page"] == page && state["shared-model-count"] == "2" && state["shared-default"] == modelSmokeDefault && state["shared-default-name"] == modelSmokeName && state["shared-default-reasoning"] == "high"
		}); err != nil {
			return err
		}
	}
	if err := d.withUI(func() error { return d.ui.SmokeAction("verify-shared-agents", modelSmokeName) }); err != nil {
		return err
	}
	passed("primary-navigation-preserves-shared-models")
	previous, readErr := d.readClipboard()
	if readErr != nil {
		return readErr
	}
	defer func() {
		// The smoke run can be used locally; restore the user's text clipboard.
		if err := d.CopyText(previous); err == nil {
			_, _ = d.snapshot()
			_, _ = d.readClipboard()
		}
	}()
	if err := d.withUI(func() error { return d.ui.SmokeAction("copy-url", "") }); err != nil {
		return err
	}
	d.owner.mu.Lock()
	expected := fmt.Sprintf("http://127.0.0.1:%d/v1", d.owner.config.Port)
	d.owner.mu.Unlock()
	if err := desktopWait("the copy control to reach the system clipboard", func() bool {
		value, err := d.readClipboard()
		return err == nil && value == expected
	}); err != nil {
		return err
	}
	passed("native-clipboard-via-ui")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	if err := d.withUI(func() error { return d.ui.SmokeAction("configure-and-start", strconv.Itoa(port)) }); err != nil {
		return err
	}
	if err := desktopWait("the UI to start the proxy", func() bool { return d.owner.trayState().running }); err != nil {
		return err
	}
	passed("proxy-start-via-ui")
	if err := d.withUI(func() error { return d.ui.SmokeAction("edit-shared-name", modelSmokeRenamed) }); err != nil {
		return err
	}
	d.mu.Lock()
	previousFrames := d.frames
	d.mu.Unlock()
	d.dispatch("close") // The same native ActionClose used by the title bar.
	if err := desktopWait("the native window to close", func() bool {
		d.mu.Lock()
		hidden := d.hidden
		d.mu.Unlock()
		return !d.windowVisible() && (runtime.GOOS != "darwin" || hidden)
	}); err != nil {
		return err
	}
	if err := desktopWait("final model edit to save while the window is closed", func() bool { return d.sharedModelSmokeSaved(modelSmokeRenamed) }); err != nil {
		return err
	}
	client := http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/models", port))
	if err != nil {
		return fmt.Errorf("proxy stopped when closing the window: %w", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("expected live proxy authentication gate, got %d", response.StatusCode)
	}
	passed("close-keeps-proxy")
	d.dispatch("open") // Exactly the tray's Open callback.
	if err := desktopWait("a newly rendered window with the same live session", func() bool {
		d.mu.Lock()
		fresh := d.window != nil && d.frames > previousFrames
		d.mu.Unlock()
		if !fresh || !d.windowVisible() {
			return false
		}
		state, err := d.snapshot()
		return err == nil && state["content-ready"] == "true" && state["status"] == "running"
	}); err != nil {
		return err
	}
	passed("tray-reopen-preserves-session")
	if err := d.withUI(func() error { return d.ui.SmokeAction("verify-shared-agents", modelSmokeRenamed) }); err != nil {
		return err
	}
	if state, err := d.snapshot(); err != nil || state["shared-default-name"] != modelSmokeRenamed || state["shared-default-reasoning"] != "high" {
		return errors.New("reopening the native window lost the final shared model edit")
	}
	passed("model-edit-survives-window-close")
	d.dispatch("stop")
	if err := desktopWait("tray stop", func() bool { return !d.owner.trayState().running }); err != nil {
		return err
	}
	passed("tray-stop")
	return nil
}
