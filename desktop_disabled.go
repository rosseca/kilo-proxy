//go:build !desktop

package main

import "errors"

func (a *app) desktopTestGateway() func() { return func() {} }

func (a *app) runDesktop(panelURL string, hidden bool, selfTest string, cleanup func()) error {
	return errors.New("this binary was built without the desktop window; build with -tags desktop or use --browser / --no-tray")
}
