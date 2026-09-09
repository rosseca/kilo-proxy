# Shared model library

The native app has one **Models** library for all agents. Add models once, set short names and reasoning preferences, and return to **Agents** to open an installed client. The library is saved automatically and restored when Kilo Proxy restarts. Generated agent profiles are updated when you next open or prepare that agent; an already running agent does not receive live configuration updates.

## Choose and edit models

1. Open **Models → Add models**. Search the catalog, filter by lab, or add an exact gateway model ID. Up to 50 models can be selected.
2. Choose **Done** to return to your library. Use each card's **Edit** controls for its name; choose the default model and supported reasoning preferences. Reorder your library using its move controls.
3. Check the save status. **Saved automatically** means the library reached disk; **Saving…**, a write error or a recovery notice needs attention before it is ready.
4. Open **Agents** and choose **Open Codex**, **Open Claude Code**, or another installed agent. Integration details and manual preparation are under that agent's **Options → Integration settings**.

Catalog sorting controls the discovery view. Library order is a separate saved preference. Changing either does not silently change your default model. The gateway ID stays exact: a short name is a display label, not a model substitution. Missing catalog entries retain their saved IDs and preferences; catalog availability does not prove protocol support or access to inference.

## What each agent receives

| Agent | Shared settings and limits |
| --- | --- |
| Codex Desktop and CLI | Names, default model and supported reasoning levels. Each has a separate generated profile; the default is put first in its exported catalog. Desktop opens the GUI, CLI opens an interactive terminal. |
| Claude Code | Names and default model, with native picker/effort behavior limited by the detected version and model. Unsupported Codex reasoning levels are not forwarded as invented Claude settings. Older versions use aliases and a global initial effort. |
| OpenCode and Zed | Model IDs, names, default and token limits. Reasoning remains automatic; the library's Codex effort preferences are not exported as equivalent native controls. The client controls its final model-picker ordering. |
| Cursor | The exact model list is published when you explicitly connect its HTTPS tunnel. Disconnect and reconnect to publish library changes. Names and reasoning controls remain subject to Cursor's own behavior. |
| Xcode | Chat, Codex and Claude derive their selections from the library, with protocol and installed-version restrictions. Xcode controls its active picker. Older bundled Claude versions can require reducing the selection to their supported alias limit. |
| Other clients | Connection guidance includes the library's default model ID. Configure the client according to its supported protocol. |

Profiles keep their own readiness state, paths, credentials and integration details. A model must support the protocol used by the chosen agent: Responses for Codex, Anthropic Messages for Claude, and Chat Completions for OpenCode/Zed/Cursor/Xcode Chat. Preparing or opening an agent does not perform an inference compatibility test.

## Saved files

The common library is `models.json` in the application configuration directory:

| Platform | Default path |
| --- | --- |
| macOS | `~/Library/Application Support/kilo-proxy/models.json` |
| Windows | `%APPDATA%\kilo-proxy\models.json` |
| Linux | `${XDG_CONFIG_HOME:-~/.config}/kilo-proxy/models.json` |

`--config-dir PATH` overrides that directory. This file is separate from generated catalogs such as `~/.codex-kilo-desktop/models.json`.

The library stores a schema version, ordered exact IDs, custom names, the default model, reasoning preferences/customizations, and token limits. It contains no API keys, prompts, request history, catalog prices or cached credentials. Credentials continue to use the existing settings, credential store and generated-profile rules described in [client setup](clients.md).

Writes validate the library, use private temporary files and replace the destination atomically. `models.json.bak` preserves a recoverable selection. A damaged file is reported instead of silently resetting the library; review the restored choices and use **Recover this selection**. Recovery preserves damaged originals under unique `.corrupt-…json` names. A failed write or conflicting external change leaves an explicit error and does not report the draft as saved.

An empty new library does not pick an arbitrary existing agent profile. Use **Models → Import an existing agent selection**, choose a source, review it, then accept it to replace the shared library.

Project choices are separate UI preferences in `agent-preferences.json`. Each agent remembers its folder, with up to six recent folders and an optional Codex application path. **Choose folder** opens the platform folder chooser. macOS and Windows use system dialogs; Linux uses an installed zenity or kdialog. A missing chooser leaves a clear path-entry fallback in **Options**.

## Optional browser helper

The `--browser` interface is a separate, older workflow. Its client tabs still maintain independent selections and explicit profile preparation; they do not edit the native shared library. To bring one of those saved profiles into the native app, use the explicit import flow above. Browser tests validate that interface separately from native library and launch tests.
