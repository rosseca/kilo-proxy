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
	"syscall"
	"time"
)

//go:embed VERSION
var embeddedVersion string

var version = strings.TrimSpace(embeddedVersion)

func randomKey(prefix string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}

func main() {
	noBrowser := flag.Bool("no-browser", false, "Do not open the local control panel")
	noTray := flag.Bool("no-tray", false, "Run without a system tray icon (headless mode)")
	configDir := flag.String("config-dir", "", "Override the application configuration directory")
	showVersion := flag.Bool("version", false, "Print version")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	if *configDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Cannot locate configuration directory:", err)
			os.Exit(1)
		}
		*configDir = filepath.Join(base, "kilo-proxy")
	}
	app, err := newApp(*configDir, systemVault{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot load Kilo Local:", err)
		os.Exit(1)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	app.adminHost = listener.Addr().String()
	admin := &http.Server{Handler: app.adminHandler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	go func() { _ = admin.Serve(listener) }()
	panelURL := "http://" + app.adminHost + "/#" + app.adminToken
	fmt.Printf("Kilo Local %s\nControl panel: %s\nUse the panel or tray menu to start the proxy. Closing the browser does not stop it.\n", version, panelURL)
	if !*noBrowser {
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
	if !*noTray {
		// The tray library locks the startup OS thread; its native loop must run here.
		if err := app.runTray(panelURL); err != nil {
			fmt.Fprintln(os.Stderr, "System tray error (headless mode: --no-tray):", err)
		}
	}
	<-app.quit
	app.cancelLogin()
	app.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = admin.Shutdown(ctx)
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
