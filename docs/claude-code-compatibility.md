# Claude Code compatibility verification

Investigated and verified on September 7, 2026, for version 0.15.0, then named Kilo Local and now called Kilo Proxy.

## Supported setup

The helper prepares an isolated `~/.claude-kilo` profile and a scoped terminal launcher on macOS, Linux and Windows. It detects Claude Code with `claude --version`, including the standard native installation path when the tray application's PATH does not include it. If detection fails, basic compatibility is used rather than claiming modern features are installed.

The installed native CLI was updated from 2.1.39 to 2.1.263 with the official `claude update` command during verification. No account credentials were copied to the test profiles.

The generated settings use documented features:

- `modelPicker` with ordered options and labels, from 2.1.242.
- `modelSettings` with per-model effort, from 2.1.251.
- `modelOverrides` for recognized native Claude IDs to preserve the original Kilo gateway route.
- Environment-variable authentication and alias defaults for internal models.
- `CLAUDE_CONFIG_DIR` plus `--settings` to keep the normal profile independent.

Unknown models keep their exact IDs and receive no invented reasoning capabilities. Anthropic does not officially support non-Claude models in Claude Code. The application documents the limits rather than translating Codex effort levels into unsupported Claude settings.

## Runtime evidence

The actual helper-written settings were copied into a disposable profile, replacing only the URL and local credential with a loopback mock gateway. Claude Code 2.1.263 loaded that profile, made an Anthropic Messages request with `model: anthropic/claude-fable-5.1`, sent the selected `output_config.effort: low`, and authenticated with the mock local bearer token. The request included `x-claude-code-session-id`, which Kilo Proxy now recognizes for per-conversation usage groups.

A separate stdio initialization returned the custom native model list with labels **Fable** and **Opus**. It advertised low/medium/high/xhigh/max for Fable and low/medium/high/max for Opus 4.6. The native Default row remains available, as documented; the lineup is not an access-control policy.

An earlier runtime test showed that passing a dotted gateway ID directly could leave Fable at high effort despite the saved low preference. The native ID plus `modelOverrides` resolved that mismatch while retaining the exact gateway request ID.

Run `python3 scripts/check-claude-profile.py /absolute/path/to/claude` to repeat a simulated Messages request with freshly generated helper settings. This test does not contact Kilo or perform paid inference.

## UI and automated checks

Browser checks covered version detection, the compatibility message, direct multi-selection, display names, per-model effort, initial model, setup, updates, exact backups, load, independent Codex selections, and English/Spanish layouts. Go tests cover profile creation and validation, merge behavior, invalid JSON, unsafe destinations, authentication, version thresholds and alias mapping. Launcher tests verify Unix process isolation and PowerShell environment restoration.

Cross-platform packaging and CI do not prove the native tray, credential store or Claude installation on every end-user machine. Model listing and simulated requests do not certify Kilo balance, permissions, Messages support or every Claude feature.

## Remaining differences from Codex GUI

Claude Code's native effort set is model-dependent; none, minimal, ultra and arbitrary custom levels cannot be imported from Codex. Persistent settings do not accept max. Fable 5, Opus 4.7 and Opus 4.8 may hold a first-use default until an explicit effort selection.

The helper does not edit managed policy or grant access to restricted models. It does not configure a second Claude Desktop application. Catalog prices stay in the helper; Claude's internal cost estimates can differ from costs reported by Kilo. Costs without gateway billing data remain unknown. Older clients without a session header remain unassigned in the usage breakdown.

## Primary references

- [Model picker and per-model settings](https://code.claude.com/docs/en/settings-reference#modelpicker)
- [Model configuration, effort and model overrides](https://code.claude.com/docs/en/model-config)
- [Settings precedence and isolated configuration](https://code.claude.com/docs/en/settings)
- [Gateway connection](https://code.claude.com/docs/en/llm-gateway-connect)
- [Gateway formats, headers and session attribution](https://code.claude.com/docs/en/llm-gateway-protocol)
