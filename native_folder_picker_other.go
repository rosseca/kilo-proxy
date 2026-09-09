//go:build desktop && !darwin && !windows

package main

import (
	"errors"
	"os/exec"
	"path/filepath"
)

func chooseNativeFolder(initial string) (string, error) {
	if path, err := exec.LookPath("zenity"); err == nil {
		return nativePickerOutput(exec.Command(path, "--file-selection", "--directory", "--title=Choose a project folder", "--filename="+initial+string(filepath.Separator)))
	}
	if path, err := exec.LookPath("kdialog"); err == nil {
		return nativePickerOutput(exec.Command(path, "--getexistingdirectory", initial, "--title", "Choose a project folder"))
	}
	return "", errors.New("No folder chooser is available. Enter a project folder path in Options, or install zenity or kdialog.")
}

func chooseNativeApplication() (string, error) {
	if path, err := exec.LookPath("zenity"); err == nil {
		return nativePickerOutput(exec.Command(path, "--file-selection", "--title=Locate Codex Desktop"))
	}
	if path, err := exec.LookPath("kdialog"); err == nil {
		return nativePickerOutput(exec.Command(path, "--getopenfilename", "", "", "--title", "Locate Codex Desktop"))
	}
	return "", errors.New("No application chooser is available. Enter the Codex application path in Options.")
}
