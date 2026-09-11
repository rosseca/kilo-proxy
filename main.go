package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed VERSION
var embeddedVersion string

// Leave the linker target uninitialized so -X main.version can override it
// for a local preview without changing the checked-in release version.
var version string

func init() {
	if version == "" {
		version = strings.TrimSpace(embeddedVersion)
	}
}

func randomKey(prefix string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}

func main() {
	if handled, code := runOpenDesignOpenCodeShim(); handled {
		os.Exit(code)
	}
	if len(os.Args) > 1 && os.Args[1] == terminalAgentFlag {
		os.Exit(runTerminalAgentMode(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == clientLaunchRunnerFlag {
		os.Exit(runClientLaunchMode(os.Args[2:]))
	}
	noBrowser := flag.Bool("no-browser", false, "Start with the window hidden (also suppresses --browser launch)")
	noTray := flag.Bool("no-tray", false, "Run headlessly without a window or system tray")
	useBrowser := flag.Bool("browser", false, "Use the system browser instead of the desktop window")
	selfTest := flag.String("desktop-self-test", "", "Run isolated native desktop checks and write a JSON report")
	configDir := flag.String("config-dir", "", "Override the application configuration directory")
	showVersion := flag.Bool("version", false, "Print version")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	isolatedProfile := ""
	if *selfTest != "" {
		// Diagnostic mode owns a fresh profile and never reads the user's vault.
		isolated, err := os.MkdirTemp("", "kilo-desktop-check-")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer os.RemoveAll(isolated)
		isolatedProfile = isolated
		*configDir = isolated
		*noBrowser, *noTray, *useBrowser = false, false, false
	}
	if *configDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Cannot locate configuration directory:", err)
			os.Exit(1)
		}
		*configDir = filepath.Join(base, "kilo-proxy")
	}
	absoluteConfig, err := filepath.Abs(*configDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot locate the Kilo Proxy configuration directory.")
		os.Exit(1)
	}
	*configDir = absoluteConfig
	app, err := newApp(*configDir, systemVault{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot load Kilo Proxy:", err)
		os.Exit(1)
	}
	stopFakeGateway := func() {}
	if *selfTest != "" {
		app.launcher = &clientLaunchRuntime{home: isolatedProfile}
		stopFakeGateway = app.desktopTestGateway()
		defer stopFakeGateway()
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	app.adminHost = listener.Addr().String()
	admin := &http.Server{Handler: app.adminHandler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	go func() { _ = admin.Serve(listener) }()
	cleanupTerminal := func() {}
	if *selfTest == "" && terminalPlatformSupported(runtime.GOOS) {
		cleanupTerminal, err = app.publishTerminalRuntime()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Terminal commands unavailable: cannot publish the private local connection file.")
		}
	}
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			cleanupTerminal()
			app.requestQuit()
			app.cancelLogin()
			app.stop()
			stopFakeGateway()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = admin.Shutdown(ctx)
			if isolatedProfile != "" {
				_ = os.RemoveAll(isolatedProfile)
			}
		})
	}
	defer cleanup()
	panelURL := "http://" + app.adminHost + "/#" + app.adminToken
	fmt.Printf("Kilo Proxy %s\nControl panel: %s\nClosing the window keeps the proxy running. Use Quit to stop the application.\n", version, panelURL)
	if *useBrowser && !*noBrowser {
		if err := openBrowser(panelURL); err != nil {
			fmt.Fprintln(os.Stderr, "Open the control panel URL above in your browser.")
		}
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		select {
		case <-signals:
			app.requestQuit()
		case <-app.quit:
		}
	}()
	if !*noTray && !*useBrowser {
		if err := app.runDesktop(panelURL, *noBrowser, *selfTest, cleanup); err != nil {
			fmt.Fprintln(os.Stderr, "Desktop startup failed:", err)
			cleanup()
			os.Exit(1)
		}
		return
	}
	<-app.quit
}

func (a *app) requestQuit() {
	a.quitOnce.Do(func() { close(a.quit) })
}

func openBrowser(address string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", address)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", address)
	default:
		cmd = exec.Command("xdg-open", address)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
