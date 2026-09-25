package main

// Installation guidance is a fixed link to the client's own documentation.
// Discovery never runs installers, probes CLI versions, or prepares profiles.
func launchClientInstallURL(id string) string {
	switch id {
	case "codex-cli":
		return "https://developers.openai.com/codex/cli/"
	case "claude":
		return "https://code.claude.com/docs/en/setup#install-claude-code"
	case "opencode":
		return "https://opencode.ai/docs/#install"
	case "omp":
		return "https://github.com/can1357/oh-my-pi#install"
	default:
		return ""
	}
}
