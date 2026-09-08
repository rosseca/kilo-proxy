// SPDX-License-Identifier: Unlicense OR MIT
//go:build darwin

package app

import "time"

func (d *displayLink) runTimer() {
	var ticker *time.Ticker
	var ticks <-chan time.Time
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()
	for {
		select {
		case start := <-d.states:
			if start && ticker == nil {
				ticker = time.NewTicker(time.Second / 60)
				ticks = ticker.C
			}
			if !start && ticker != nil {
				ticker.Stop()
				ticker = nil
				ticks = nil
			}
		case <-ticks:
			d.callback()
		case <-d.dids: // A timer does not track a physical display.
		case <-d.done:
			return
		}
	}
}
