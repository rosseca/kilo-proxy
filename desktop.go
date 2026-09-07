//go:build desktop

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
	gioapp "gioui.org/app"
	"gioui.org/io/clipboard"
	gioevent "gioui.org/io/event"
	"gioui.org/io/system"
	"gioui.org/io/transfer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

type nativeDesktop struct {
	owner            *app
	ui               *nativeUI
	cleanup          func()
	actions          chan string
	trayReady        chan struct{}
	trayOnce         sync.Once
	firstFrameOnce   sync.Once
	firstFrameReady  chan struct{}
	trayEnd          func()
	items            map[string]*systray.MenuItem
	mu               sync.Mutex
	window           *gioapp.Window
	windowDone       chan struct{}
	nativeView       uintptr
	hidden           bool
	frames           uint64
	generation       uint64
	jobs             []func()
	copyPending      []string
	clipboardRead    chan string
	clipboardWaiting chan string
	clipboardTag     struct{}
	last             trayState
	message          string
	quitLabel        string
	exitCode         int
}

// CopyText schedules the OS clipboard operation on the next native frame. It
// does not depend on a browser API or a separate clipboard executable.
func (d *nativeDesktop) CopyText(text string) error {
	d.mu.Lock()
	if d.window == nil {
		d.mu.Unlock()
		return errors.New("Open the Kilo Local window before copying")
	}
	if len(d.copyPending) >= 32 {
		d.mu.Unlock()
		return errors.New("The clipboard is busy; try again")
	}
	d.copyPending = append(d.copyPending, text)
	w := d.window
	d.mu.Unlock()
	w.Invalidate()
	return nil
}

func (d *nativeDesktop) OpenExternal(address string) error { return openBrowser(address) }

func (d *nativeDesktop) invalidate() {
	d.mu.Lock()
	w := d.window
	d.mu.Unlock()
	if w != nil {
		w.Invalidate()
	}
}

func (d *nativeDesktop) dispatch(action string) {
	select {
	case d.actions <- action:
	case <-d.owner.quit:
	default:
	}
}

func (d *nativeDesktop) show() {
	d.mu.Lock()
	if w := d.window; w != nil {
		d.hidden = false
		d.mu.Unlock()
		w.Option(gioapp.Windowed.Option())
		w.Perform(system.ActionRaise)
		w.Invalidate()
		return
	}
	w := new(gioapp.Window)
	d.window = w
	d.hidden = false
	d.windowDone = make(chan struct{})
	d.generation++
	done := d.windowDone
	d.mu.Unlock()
	go d.runWindow(w, done)
}

func (d *nativeDesktop) runWindow(w *gioapp.Window, done chan struct{}) {
	defer close(done)
	defer func() {
		d.mu.Lock()
		if d.window == w {
			d.window = nil
		}
		d.mu.Unlock()
	}()
	w.Option(gioapp.Title("Kilo Local"), gioapp.Size(unit.Dp(1180), unit.Dp(820)), gioapp.MinSize(unit.Dp(720), unit.Dp(560)))
	var ops op.Ops
	var nativeViewEvent gioapp.ViewEvent
	lifecycleInstalled := false
	for {
		switch e := w.Event().(type) {
		case gioapp.DestroyEvent:
			if e.Err != nil {
				fmt.Fprintln(os.Stderr, "Native window failed:", e.Err)
				d.mu.Lock()
				d.exitCode = 1
				d.mu.Unlock()
				d.owner.requestQuit()
			}
			return
		case gioapp.ViewEvent:
			if e.Valid() && runtime.GOOS == "darwin" {
				nativeViewEvent = e
				// Cocoa has one application loop. Register on Gio's native
				// thread, without replacing its application delegate.
				d.trayOnce.Do(func() {
					w.Run(func() {
						start, end := systray.RunWithExternalLoop(d.setupTray, func() {})
						d.trayEnd = end
						start()
					})
				})
			}
		case gioapp.FrameEvent:
			// AppKit emits ViewEvent while setContentView is still executing,
			// before Gio assigns the NSWindow delegate. Install the close hook
			// after creation completes, at the first real native frame.
			if runtime.GOOS == "darwin" && !lifecycleInstalled && nativeViewEvent != nil {
				w.Run(func() { installNativeLifecycle(d, nativeViewEvent) })
				lifecycleInstalled = true
			}
			gtx := gioapp.NewContext(&ops, e)
			d.processFrame(gtx)
			d.ui.Layout(gtx)
			e.Frame(gtx.Ops)
			d.mu.Lock()
			d.frames++
			d.mu.Unlock()
			d.firstFrameOnce.Do(func() { close(d.firstFrameReady) })
		}
	}
}

func (d *nativeDesktop) processFrame(gtx layout.Context) {
	gioevent.Op(gtx.Ops, &d.clipboardTag)
	for {
		e, ok := gtx.Event(transfer.TargetFilter{Target: &d.clipboardTag, Type: "text/plain"}, transfer.TargetFilter{Target: &d.clipboardTag, Type: "application/text"})
		if !ok {
			break
		}
		if data, ok := e.(transfer.DataEvent); ok {
			stream := data.Open()
			text, err := io.ReadAll(io.LimitReader(stream, 1<<20))
			_ = stream.Close()
			d.mu.Lock()
			waiting := d.clipboardWaiting
			d.clipboardWaiting = nil
			d.mu.Unlock()
			if waiting != nil && err == nil {
				select {
				case waiting <- string(text):
				default:
				}
			}
		}
	}
	d.mu.Lock()
	jobs, copies, read := d.jobs, d.copyPending, d.clipboardRead
	d.jobs, d.copyPending, d.clipboardRead = nil, nil, nil
	if read != nil {
		d.clipboardWaiting = read
	}
	d.mu.Unlock()
	for _, job := range jobs {
		job()
	}
	for _, text := range copies {
		gtx.Execute(clipboard.WriteCmd{Type: "text/plain", Data: io.NopCloser(strings.NewReader(text))})
	}
	if read != nil {
		gtx.Execute(clipboard.ReadCmd{Tag: &d.clipboardTag})
	}
}

func trayICO(png []byte) []byte {
	// ICO with an embedded PNG uses Windows' built-in icon decoder.
	result := make([]byte, 22, 22+len(png))
	result[2], result[4], result[6], result[7], result[10], result[12], result[18] = 1, 1, 44, 44, 1, 32, 22
	size := uint32(len(png))
	for i := 0; i < 4; i++ {
		result[14+i] = byte(size >> (8 * i))
	}
	return append(result, png...)
}

func (d *nativeDesktop) setupTray() {
	if runtime.GOOS == "darwin" {
		systray.SetTemplateIcon(trayIcon(true), trayIcon(false))
	} else if runtime.GOOS == "windows" {
		systray.SetIcon(trayICO(trayIcon(false)))
	} else {
		systray.SetIcon(trayIcon(false))
	}
	systray.SetTitle("Kilo Local")
	d.items = map[string]*systray.MenuItem{}
	systray.AddMenuItem("Kilo Local · "+version, "").Disable()
	for _, key := range []string{"status", "team", "address", "activity"} {
		d.items[key] = systray.AddMenuItem(" ", "")
		d.items[key].Disable()
	}
	systray.AddSeparator()
	for _, key := range []string{"open", "start", "stop"} {
		d.items[key] = systray.AddMenuItem(" ", "")
	}
	d.items["message"] = systray.AddMenuItem(" ", "")
	d.items["message"].Disable()
	systray.AddSeparator()
	d.items["quit"] = systray.AddMenuItem("Quit Kilo Local", "")
	for _, key := range []string{"open", "start", "stop", "quit"} {
		action, clicked := key, d.items[key].ClickedCh
		go func() {
			for {
				select {
				case <-clicked:
					d.dispatch(action)
				case <-d.owner.quit:
					return
				}
			}
		}()
	}
	close(d.trayReady)
}

func (d *nativeDesktop) updateTray() {
	select {
	case <-d.trayReady:
	default:
		return
	}
	s := d.owner.trayState()
	if s == d.last {
		return
	}
	d.items["status"].SetTitle(s.status)
	d.items["team"].SetTitle(s.team)
	d.items["address"].SetTitle(s.labels.endpoint + s.address)
	d.items["activity"].SetTitle(s.activity)
	d.items["open"].SetTitle(s.labels.open)
	start := s.labels.setup
	if s.configured {
		start = s.labels.start
	}
	d.items["start"].SetTitle(start)
	if s.running || s.pending {
		d.items["start"].Disable()
	} else {
		d.items["start"].Enable()
	}
	d.items["stop"].SetTitle(s.labels.stop)
	if s.running {
		d.items["stop"].Enable()
	} else {
		d.items["stop"].Disable()
	}
	d.items["quit"].SetTitle(s.labels.exit)
	d.items["message"].SetTitle(s.labels.message(d.message))
	systray.SetTooltip(s.labels.tooltip)
	d.mu.Lock()
	d.quitLabel = s.labels.exit
	d.mu.Unlock()
	d.last = s
}

func (d *nativeDesktop) control() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-d.owner.quit:
			d.cleanup()
			d.mu.Lock()
			w, done, code := d.window, d.windowDone, d.exitCode
			d.mu.Unlock()
			if w != nil {
				w.Perform(system.ActionClose)
				select {
				case <-done:
				case <-time.After(2 * time.Second):
				}
			}
			select {
			case <-d.trayReady:
				if d.trayEnd != nil {
					d.trayEnd()
				} else {
					systray.Quit()
				}
			default:
			}
			// Gio owns the startup thread indefinitely. All application
			// cleanup runs above before its documented process-exit pattern.
			os.Exit(code)
		case action := <-d.actions:
			d.message = ""
			switch action {
			case "open":
				d.show()
			case "close":
				d.mu.Lock()
				w := d.window
				d.mu.Unlock()
				if w != nil {
					w.Perform(system.ActionClose)
				}
			case "start":
				if !d.owner.trayState().configured {
					d.show()
				} else if err := d.owner.start(); err != nil {
					d.message = "start-error"
					d.show()
				}
			case "stop":
				d.owner.stop()
				d.invalidate()
			case "quit":
				d.owner.requestQuit()
			}
			d.last = trayState{}
			d.updateTray()
		case <-ticker.C:
			d.updateTray()
		}
	}
}

func (a *app) runDesktop(_ string, hidden bool, selfTest string, cleanup func()) error {
	d := &nativeDesktop{owner: a, cleanup: cleanup, actions: make(chan string, 16), trayReady: make(chan struct{}), firstFrameReady: make(chan struct{})}
	a.mu.Lock()
	a.desktop = d
	a.mu.Unlock()
	d.ui = newNativeUI(a, d.invalidate)
	if runtime.GOOS != "darwin" {
		go func() { runtime.LockOSThread(); defer runtime.UnlockOSThread(); systray.Run(d.setupTray, func() {}) }()
	}
	go d.control()
	// Cocoa initializes the tray through the first real window. Hidden startup
	// closes that window after the tray is ready while retaining application state.
	d.show()
	if hidden {
		go func() { <-d.trayReady; <-d.firstFrameReady; d.dispatch("close") }()
	}
	if selfTest != "" {
		go d.selfTest(selfTest)
	}
	gioapp.Main()
	return nil
}
