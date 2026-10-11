//go:build desktop && darwin

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

// The bundle path is passed as argv so it never becomes script source.
// NSWorkspace returns the icon Finder shows, including asset-catalog icons
// that have no .icns file.
const nativeAppIconScript = `ObjC.import('AppKit');
function run(argv) {
	var img = $.NSWorkspace.sharedWorkspace.iconForFile(argv[0]);
	var rep = $.NSBitmapImageRep.imageRepWithData(img.TIFFRepresentation);
	var png = rep.representationUsingTypeProperties($.NSBitmapImageFileTypePNG, $());
	if (!png.writeToFileAtomically(argv[1], true)) throw new Error('write failed');
}`

// nativeAppIconPNG renders an application bundle's icon as a small PNG.
func nativeAppIconPNG(bundle string) ([]byte, error) {
	if st, err := os.Stat(bundle); err != nil || !st.IsDir() {
		return nil, errors.New("application bundle not found")
	}
	dir, err := os.MkdirTemp("", "kilo-app-icon-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "icon.png")
	if err := exec.Command("/usr/bin/osascript", "-l", "JavaScript", "-e", nativeAppIconScript, bundle, out).Run(); err != nil {
		return nil, err
	}
	if err := exec.Command("/usr/bin/sips", "-Z", "256", out).Run(); err != nil {
		return nil, err
	}
	return os.ReadFile(out)
}
