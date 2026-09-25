# Shared model library

The native app has one **Models** library for all agents. Add models once, set short names and reasoning preferences, and return to **Agents** to open an installed client. The library is saved automatically and restored when Kilo Proxy restarts. Generated agent profiles are updated when you next open or prepare that agent; an already running agent does not receive live configuration updates.

## Choose and edit models

1. Open **Models → Add models**. Search the catalog, filter by lab, or add an exact gateway model ID. Up to 50 models can be selected.
2. Choose **Done** to return to your library. Use each card's **Edit** controls for its name; choose the default model and supported reasoning preferences. Reorder your library using its move controls.
3. Check the save status. **Saved automatically** means the library reached disk; **Saving…**, a write error or a recovery notice needs attention before it is ready.
4. Open **Agents** and choose **Open Codex**, **Open Claude Code**, or another installed agent. Integration details and manual preparation are under that agent's **Options → Integration settings**.

Catalog sorting controls the discovery view. Library order is a separate saved preference. Changing either does not silently change your default model. The gateway ID stays exact: a short name is a display label, not a model substitution. Missing catalog entries retain their saved IDs and preferences; catalog availability does not prove protocol support or access to inference.

## Recommended models

Setup and **Models** show a short **Recommended models** list: frontier OpenAI and Anthropic models plus popular Chinese models. In setup it appears above the catalog on the models step and lists every recommended model without an inner scroll. In **Models** it replaces the empty-library card; once you have models it sits under the **Recommended models** disclosure. Tick individual cards or choose **Add all**. Adding recommendations to an empty library makes the model marked **Suggested default** your default; an existing default is never replaced. Change the default with **Use by default** on an added card, or in setup with the **Default model** menu, which lists every selected model, including ones picked from the full catalog.

The list is [`recommended-models.json`](../recommended-models.json) in this repository. Kilo Proxy downloads it from `https://raw.githubusercontent.com/rosseca/kilo-proxy/main/recommended-models.json` when it loads the Kilo catalog, so editing the file on `main` updates installed apps without a release. The request sends no API key, organization header or cookies, uses a three-second timeout, does not follow redirects, and caches a valid list for one hour. If GitHub is unreachable or the file is invalid, the app uses the copy compiled into the binary. Custom gateways use only the compiled copy and make no GitHub request.

The file only marks models by exact gateway ID; prices and availability always come from your team's Kilo catalog, and IDs your team cannot use are not shown. Format: `schemaVersion` 1, up to 50 `models` in display order, each with an `id`, optional `note` (`en`/`es`, 160 characters), optional `reasoning` (initial level, applied only when the catalog offers it for that model) and exactly one `"default": true`. `go test ./...` validates the committed file.

## Context window presets

Choose a context preset in **Models** for the library, or override an individual model in its **Edit** controls:

| Preset | Working context window |
| --- | --- |
| **Recommended** (272K tokens) | Up to 272,000 tokens. The default for newly selected models. |
| **Low** (128K tokens) | Up to 128,000 tokens, with earlier compaction. |
| **Maximum** | The maximum published in the current or cached Kilo catalog. Unavailable when that maximum is unknown. |
| **Custom** | Your chosen token count, bounded by the known model maximum. |

**Recommended is a Kilo Proxy product preset**, not a provider guarantee or a claim that 272K is optimal for every task. Every preset respects a smaller published model window. The app shows the working window separately from the model maximum. If catalog metadata is unavailable, Recommended/Low remain explicit provisional budgets; refresh the catalog to verify capacity before relying on a manually added model.

Presets save automatically with your shared model choices. The catalog maximum is kept separate from the preference, so refreshing model metadata does not replace your selected preset. Existing saved numeric limits remain **Custom** because older files do not distinguish a manual choice from a copied catalog maximum. Apply **Recommended** to your library once to adopt the new budget for existing models.

Context includes conversation history, instructions, tools, reasoning and output. Generated output allowances are bounded to one quarter of the working window; an unspecified output allowance uses 8,192 tokens, further reduced if necessary. This leaves room for input instead of exporting, for example, 128K context with 128K output. Your saved output preference is retained separately from the effective export.

Open or prepare an agent again after editing presets, and restart an already running instance to load its changed profile. Kilo Proxy does not delete conversation messages or force a running conversation to compact. Agents manage compaction using their supported settings:

- **Codex Desktop/CLI:** per-model context and automatic-compaction metadata in the generated catalog. Managed global context overrides are removed from the isolated profile so they cannot override a different model's budget after a switch.
- **Oh My Pi, OpenCode and Zed:** per-model context and output limits. The client decides how to compact. OpenCode always receives its context limit even when output is unspecified.
- **Claude Code:** one session-wide compaction window, using the smallest selected context budget and capped at 1M. Claude may further cap it to its recognized model capacity. Its supported explicit range starts at 100K; preparing Claude with a smaller selected window reports an error instead of silently increasing it. Output also uses the smallest selected allowance. This does not provide distinct compaction windows on each `/model` switch.
- **Open Design:** inherits the behavior of its selected CLI engine.
- **Xcode Chat:** its integration does not expose an equivalent managed context budget. Xcode's Codex and Claude engines use the corresponding adapter, subject to bundled-version support.

Context tokens and HTTP payload size are different limits. These presets do not remove Kilo Gateway's request-body size limit; image upload and compression settings still apply.

## What each agent receives

| Agent | Shared settings and limits |
| --- | --- |
| Codex Desktop and CLI | Names, default model, supported reasoning levels, and per-model context/compaction budgets. Each has a separate generated profile; the default is put first in its exported catalog. Desktop opens the GUI, CLI opens an interactive terminal. |
| Claude Code | Names, default model and a session-wide context budget, with native picker/effort behavior limited by the detected version and model. Unsupported Codex reasoning levels are not forwarded as invented Claude settings. Older versions use aliases and a global initial effort. |
| Claude Desktop | Claude IDs, names and default; optional experimental aliases include other providers using their real display names. The library always stores real model IDs. Desktop controls context and reasoning; shared numeric limits are not exported. See [Desktop compatibility](claude-desktop.md). |
| Oh My Pi | Names, default model, supported reasoning, per-model context and output limits, and the optional Kilo images MCP. |
| OpenCode and Zed | Model IDs, names, default and token limits. Reasoning remains automatic; the library's Codex effort preferences are not exported as equivalent native controls. The client controls its final model-picker ordering. |
| Open Design | Private Codex CLI, Claude Code or OpenCode profiles receive the same supported shared settings as those engines. Open Design selects **CLI default** for the shared default; its own picker may not list every shared model. Quit its Kilo instance before applying model, engine or connection changes. See [setup](open-design.md). |
| Xcode | Chat, Codex and Claude derive their selections from the library, with protocol and installed-version restrictions. Xcode controls its active picker. Older bundled Claude versions can require reducing the selection to their supported alias limit. |
| Other clients | Connection guidance includes the library's default model ID. Configure the client according to its supported protocol. |

Profiles keep their own readiness state, paths, credentials and integration details. A model must support the protocol used by the chosen agent: Responses for Codex, Anthropic Messages for Claude, and Chat Completions for OpenCode/Zed/Xcode Chat. Open Design uses the protocol of its selected Local CLI engine. Kilo Proxy does not run inference to test each model when preparing a profile. The opened application can perform its own connection check; Claude Desktop probes the configured gateway on startup.

## Saved files

The common library is `models.json` in the application configuration directory:

| Platform | Default path |
| --- | --- |
| macOS | `~/Library/Application Support/kilo-proxy/models.json` |
| Windows | `%APPDATA%\kilo-proxy\models.json` |
| Linux | `${XDG_CONFIG_HOME:-~/.config}/kilo-proxy/models.json` |

`--config-dir PATH` overrides that directory. This file is separate from generated catalogs such as `~/.codex-kilo-desktop/models.json`.

The library stores a schema version, ordered exact IDs, custom names, the default model, reasoning preferences/customizations, context presets, custom context values, and output preferences. It contains no API keys, prompts, request history, catalog prices or cached credentials. Credentials continue to use the existing settings, credential store and generated-profile rules described in [client setup](clients.md).

Writes validate the library, use private temporary files and replace the destination atomically. `models.json.bak` preserves a recoverable selection. A damaged file is reported instead of silently resetting the library; review the restored choices and use **Recover this selection**. Recovery preserves damaged originals under unique `.corrupt-…json` names. A failed write or conflicting external change leaves an explicit error and does not report the draft as saved.

An empty new library does not pick an arbitrary existing agent profile. Use **Models → Import an existing agent selection**, choose a source, review it, then accept it to replace the shared library.

Project choices are separate UI preferences in `agent-preferences.json`. Each agent remembers its folder, with up to six recent folders and an optional Codex application path. **Choose folder** opens the platform folder chooser. macOS and Windows use system dialogs; Linux uses an installed zenity or kdialog. A missing chooser leaves a clear path-entry fallback in **Options**.

## Optional browser helper

The `--browser` interface is a separate, older workflow. Its client tabs still maintain independent selections and explicit profile preparation; they do not edit the native shared library. To bring one of those saved profiles into the native app, use the explicit import flow above. Browser tests validate that interface separately from native library and launch tests. Its agent exports store effective numeric limits; loading an exported selection restores those limits as Custom. The native shared library preserves the named preset itself.
