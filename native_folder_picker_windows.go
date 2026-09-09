//go:build desktop && windows

package main

import (
	"os"
	"os/exec"
)

func chooseNativeFolder(initial string) (string, error) {
	script := `Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.FolderBrowserDialog
$dialog.Description = 'Choose a project folder'
$dialog.SelectedPath = $env:KILO_PICKER_INITIAL
if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8; Write-Output $dialog.SelectedPath } else { exit 1 }`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-Command", script)
	cmd.Env = append(os.Environ(), "KILO_PICKER_INITIAL="+initial)
	return nativePickerOutput(cmd)
}

func chooseNativeApplication() (string, error) {
	script := `Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.OpenFileDialog
$dialog.Title = 'Locate Codex Desktop'
$dialog.Filter = 'Applications (*.exe)|*.exe'
if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8; Write-Output $dialog.FileName } else { exit 1 }`
	return nativePickerOutput(exec.Command("powershell.exe", "-NoProfile", "-STA", "-Command", script))
}
