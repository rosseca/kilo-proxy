//go:build desktop && !darwin

package main

import "errors"

// Other platforms keep the monograms.
func nativeAppIconPNG(string) ([]byte, error) {
	return nil, errors.New("application icons are only read on macOS")
}
