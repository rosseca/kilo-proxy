//go:build !windows

package main

func prepareTerminalAgentConsole() (func(), error) { return func() {}, nil }
