//go:build !darwin

package systray

// TrayAppearance is only available for the macOS status button.
func TrayAppearance() (title string, hasIcon, supported bool) { return "", false, false }
