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
	case "openmausbot":
		return "https://github.com/milind-soni/OpenMausBot/releases"
	case "synara":
		return "https://github.com/Emanuele-web04/synara/releases/tag/v1.0.0-beta.1"
	case "t3-code":
		return "https://github.com/pingdotgg/t3code/releases/tag/v0.0.45"
	default:
		return ""
	}
}
