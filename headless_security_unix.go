//go:build !windows

package main

import (
	"os"
	"syscall"
)

func headlessFileOwned(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Geteuid())
}
