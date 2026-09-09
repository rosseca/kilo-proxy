//go:build !darwin

package systray

// ClearIcon leaves other platforms' required tray icon intact.
func ClearIcon() {}

// TrayAppearance is only available for the macOS status button.
func TrayAppearance() (title string, hasIcon, supported bool) { return "", false, false }
