//go:build desktop && darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit
void kiloInstallLifecycle(void *view);
int kiloWindowVisible(void *view);
*/
import "C"

import (
	gioapp "gioui.org/app"
	"sync"
	"unsafe"
)

var nativeLifecycle struct {
	sync.Mutex
	desktop *nativeDesktop
}

func installNativeLifecycle(d *nativeDesktop, event gioapp.ViewEvent) {
	view := event.(gioapp.AppKitViewEvent).View
	d.mu.Lock()
	d.nativeView = view
	d.mu.Unlock()
	nativeLifecycle.Lock()
	nativeLifecycle.desktop = d
	nativeLifecycle.Unlock()
	C.kiloInstallLifecycle(unsafe.Pointer(view))
}

//export kiloNativeQuit
func kiloNativeQuit() {
	nativeLifecycle.Lock()
	d := nativeLifecycle.desktop
	nativeLifecycle.Unlock()
	if d != nil {
		d.owner.requestQuit()
	}
}

//export kiloNativeReopen
func kiloNativeReopen() {
	nativeLifecycle.Lock()
	d := nativeLifecycle.desktop
	nativeLifecycle.Unlock()
	if d != nil {
		d.dispatch("open")
	}
}

//export kiloNativeHide
func kiloNativeHide() {
	nativeLifecycle.Lock()
	d := nativeLifecycle.desktop
	nativeLifecycle.Unlock()
	if d != nil {
		d.mu.Lock()
		d.hidden = true
		d.mu.Unlock()
	}
}

func (d *nativeDesktop) windowVisible() bool {
	d.mu.Lock()
	w, view := d.window, d.nativeView
	d.mu.Unlock()
	if w == nil || view == 0 {
		return false
	}
	visible := false
	w.Run(func() { visible = C.kiloWindowVisible(unsafe.Pointer(view)) != 0 })
	return visible
}
