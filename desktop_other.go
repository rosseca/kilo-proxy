//go:build desktop && !darwin

package main

import gioapp "gioui.org/app"

func installNativeLifecycle(*nativeDesktop, gioapp.ViewEvent) {}
func (d *nativeDesktop) windowVisible() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.window != nil
}
