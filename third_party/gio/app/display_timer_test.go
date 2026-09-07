// SPDX-License-Identifier: Unlicense OR MIT
//go:build darwin

package app

import (
	"testing"
	"time"
)

func TestFallbackTimerStartsStopsAndCloses(t *testing.T) {
	frames := make(chan struct{}, 16)
	finished := make(chan struct{})
	d := &displayLink{callback: func() {
		select {
		case frames <- struct{}{}:
		default:
		}
	}, states: make(chan bool), done: make(chan struct{}), dids: make(chan uint64)}
	go func() { d.runTimer(); close(finished) }()
	d.Start()
	select {
	case <-frames:
	case <-time.After(time.Second):
		t.Fatal("timer did not request a frame")
	}
	d.SetDisplayID(42)
	d.Stop()
	// Stopping is synchronous; drain any callback queued before the stop.
	time.Sleep(25 * time.Millisecond)
	for len(frames) > 0 {
		<-frames
	}
	select {
	case <-frames:
		t.Fatal("timer rendered while idle")
	case <-time.After(50 * time.Millisecond):
	}
	d.Start()
	select {
	case <-frames:
	case <-time.After(time.Second):
		t.Fatal("timer did not restart")
	}
	d.Close()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("timer leaked after close")
	}
}
