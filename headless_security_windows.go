//go:build windows

package main

import "os"

// The server CLI is supported only on Unix. Keep non-server desktop builds
// portable; no Windows invocation selects the file-backed headless vault.
func headlessFileOwned(os.FileInfo) bool { return false }
