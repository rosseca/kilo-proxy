//go:build desktop

package main

// Recheck executable discovery and, for Claude Code, the capabilities used by
// profile generation. No profile is prepared and no installer is run here.
func (u *nativeUI) refreshAgentInstallation(key string) {
	u.detectLaunchers()
	if key == "claude" && !u.busy["GET/api/claude/info"] {
		u.clientState().ClaudeDetectStarted = false
		u.detectAgentCapabilities()
	}
}
