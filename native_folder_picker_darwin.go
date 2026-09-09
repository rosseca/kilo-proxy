//go:build desktop && darwin

package main

import "os/exec"

// Pass paths as argv; project names can never become AppleScript source.
func chooseNativeFolder(initial string) (string, error) {
	script := `on run argv
set promptText to "Choose a project folder"
if (count of argv) > 0 and item 1 of argv is not "" then
try
set initialFolder to POSIX file (item 1 of argv) as alias
return POSIX path of (choose folder with prompt promptText default location initialFolder)
on error number -43
return POSIX path of (choose folder with prompt promptText)
end try
end if
return POSIX path of (choose folder with prompt promptText)
end run`
	return nativePickerOutput(exec.Command("/usr/bin/osascript", "-e", script, "--", initial))
}

func chooseNativeApplication() (string, error) {
	return nativePickerOutput(exec.Command("/usr/bin/osascript", "-e", `POSIX path of (choose file with prompt "Locate Codex Desktop" of type {"com.apple.application-bundle"} default location (POSIX file "/Applications"))`))
}
