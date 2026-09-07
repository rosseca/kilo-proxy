# Client setup

Start Kilo Local and use the control panel to generate configuration for your selected models. Merge JSON snippets into existing settings rather than replacing the entire file. The local key grants access to your organization’s credits while the proxy is running; do not commit generated credentials.

## Codex Desktop: a second GUI instance

The **Codex Desktop** tab launches the installed desktop application with separate configuration and interface data, allowing your normal Codex to remain open.

1. Create `~/.codex-kilo-desktop` (`%USERPROFILE%\.codex-kilo-desktop` on Windows) and save the generated `config.toml` there.
2. Choose the operating system and actual installed app path in the helper. On macOS this may be `/Applications/ChatGPT.app` or `/Applications/Codex.app`; select the bundle that contains Codex.
3. Copy and execute the generated launch command. It supplies `CODEX_HOME`, `CODEX_ELECTRON_USER_DATA_PATH`, `--user-data-dir`, and `KILO_LOCAL_API_KEY` to the new instance. macOS uses `open -n`.

Interface data lives in `~/Library/Application Support/Codex Kilo` on macOS, `%LOCALAPPDATA%\Codex Kilo` on Windows, or `${XDG_CONFIG_HOME:-~/.config}/codex-kilo-desktop` on Linux. Relaunching the same isolated profile may focus its existing window.

Keep the normal `~/.codex/config.toml` unchanged. Do not copy its `auth.json`, cookies, or history. The generated provider uses the local proxy key and `cli_auth_credentials_store = "file"`. Profiles isolate settings and history, not workspaces: use separate checkouts or worktrees for concurrent edits to the same repository. If you previously edited your main config, restore its original provider/model manually.

The Electron isolation variable is version-dependent rather than a stable public API. [Compatibility notes](codex-desktop-compatibility.md) describe the inspected macOS version and what was tested. The original vendor app is installed separately and is not redistributed here. Windows and Linux require verification with the installed app version.

### Multiple models, reasoning, and short names

1. Check models in the catalog, or select search results in bulk, up to 50. **Selected only** filters the same list. Manual IDs are under **Add by ID**.
2. Adjust reasoning in the selected row and choose **Use on startup** for the initial model. **Customize levels** changes that model’s available efforts; **Use suggested levels** restores known defaults.
3. Edit **Name in Codex** for a short display name such as Sol, GLM, or Fable. Clearing it restores the catalog label. Names are limited to 80 characters and searchable.
4. Click **Save to Codex Kilo**, or download the catalog for another computer. Saving atomically updates only `~/.codex-kilo-desktop/models.json`, with a `.bak` backup. The profile’s `config.toml` must already exist.
5. On initial setup, or when changing the initial model, also copy the generated `config.toml`. Restart Codex Kilo to load the changes.

**Load saved catalog** restores models, labels, and reasoning. Unsaved changes last only for the panel session. Saving does not modify credentials or `config.toml`. The config references `model_catalog_json = "models.json"`; the exporter puts the initial model first because the inspected app-server’s `model/list` default follows catalog order.

Model IDs remain unchanged. Renaming changes only `display_name`; reasoning is sent as `reasoning.effort`. Explicit efforts from Kilo’s `opencode.variants` metadata take priority over exact-ID fallback presets. Manual customizations take priority over both. Variant names and token budgets are not interpreted as effort levels. Unknown models receive no invented levels and can be configured manually.

Current exact-ID presets include Sol discounted (none/low/medium/high/xhigh/max, initially low), GLM 5.3 and 5.3 Flash (low/high/max, initially max), and Fable 5.1 (low/medium/high/xhigh/max, initially high). These are metadata/preset choices, not guarantees that every gateway route accepts every effort.

All models in this profile must support **Responses**. Catalog listing does not translate protocols or verify credits. Exported capabilities are conservative and coding instructions are original generic instructions, not vendor system prompts.

### “Missing environment variable”

Copy `env_key = "KILO_LOCAL_API_KEY"` literally. It names an environment variable; never replace it with the actual token. Save the TOML first, then use **Copy launch command** to pass the local key when opening Codex. Close a previously running Kilo instance before relaunching with updated environment values.

The on-screen launch preview masks the key and cannot be executed as displayed. The copy button uses the real key. **Show key in command** reveals the executable text for manual selection.

### Fable / Anthropic tool schemas

For `/v1/responses` requests to `anthropic/*` and `~anthropic/*`, the proxy adapts function schemas with root `oneOf`, `anyOf`, or `allOf`. The original schema is preserved under required `kilo_tool_input`; constraints are not removed or flattened. Namespaced tools are supported.

Historical call arguments are wrapped and new arguments are unwrapped before reaching Codex. For affected tools only, SSE argument fragments are buffered until `response.function_call_arguments.done` and emitted as one validated delta. Other events and keepalives continue streaming. Invalid or incomplete wrappers stop the response rather than send malformed calls.

Local JSON Pointer references are relocated. Schemas with IDs, anchors, external references, or dynamic references are rejected because they require additional resolution. Requests, adapted JSON responses, and individual SSE events have a 32 MiB limit. This is schema adaptation, not a Responses-to-Messages translator; protocol support still depends on Kilo.

## Codex CLI

The **Codex CLI** tab has its own model choice and terminal launcher using `~/.codex-kilo-cli`. Save its generated `config.toml` before running the command. It uses Responses and the local key, and does not replace either desktop profile. Select the Desktop tab whenever you want the GUI.

## OpenCode

Add multiple models in the **OpenCode** tab and choose the initial model. Merge the generated custom provider into your OpenCode configuration. Use `/connect`, choose **Other**, and supply the local key for the configured provider. Use `/models` to switch between configured models. This helper targets OpenCode v1 and the OpenAI-compatible Chat Completions path.

## Claude Code

Merge the helper’s JSON into your user-level `~/.claude/settings.json` (`%USERPROFILE%\.claude\settings.json` on Windows). The configuration uses:

- `ANTHROPIC_BASE_URL`: `http://127.0.0.1:8877`, without `/v1`; the SDK appends it.
- `ANTHROPIC_AUTH_TOKEN`: the local key, sent as Bearer authentication.
- `ANTHROPIC_MODEL`: the initial Kilo model ID.
- `ANTHROPIC_DEFAULT_SONNET_MODEL`, `ANTHROPIC_DEFAULT_OPUS_MODEL`, and `ANTHROPIC_DEFAULT_HAIKU_MODEL`: independently assigned models, falling back to the initial model when requested.

Add several models and assign the three aliases. Switch with `/model sonnet`, `/model opus`, or `/model haiku`. For additional entries, copy `/model <full-kilo-id>` from the helper. Removing a model resets references to it. Start `claude` and check the base URL with `/status`; review setting precedence if you already define these variables elsewhere.

This template uses aliases present in the inspected Claude Code 2.1.39. It does not inject an arbitrary catalog into that version’s picker, activate newer gateway discovery / `modelPicker`, or override organization restrictions. All selected models, including aliases used by internal tasks, must support Anthropic Messages. The proxy supports `/v1/messages?beta=true` and version/beta headers, but not auxiliary endpoints such as token counting. Helper prices may differ from labels in older Claude versions.

## Zed and Xcode

Use the OpenAI-compatible provider, the panel’s `/v1` base URL, and the local key. Select the exact model ID. Merge the generated Zed settings and adjust its context window to the chosen model. Listing models alone does not verify generation access.

## Cursor

The helper collects up to 50 model IDs, with search, prices, removal, and **Copy model IDs**. Add exact IDs individually under **Settings → Models → Add Custom Model / Add model**, then enable them in Cursor’s picker. This list is independent of Codex and is not a Codex `models.json` file.

Cursor’s backend cannot reach your computer’s `127.0.0.1`. Its base-URL override needs an externally reachable HTTPS gateway. Kilo Local is loopback-only and validates Host, so pointing Cursor at its local URL or adding a default tunnel does not establish a supported connection. The helper deliberately leaves URL/credential fields for an organization-provided reachable gateway; it deploys no remote gateway or tunnel.

Model listing is not compatibility validation. Cursor BYOK coverage, reasoning support, and protocol routing depend on its version and model; Tab uses its own models. Preserve Kilo prefixes rather than assuming custom IDs match Cursor’s native IDs.

## Sources

- [Kilo authentication](https://kilo.ai/docs/gateway/authentication), [API reference](https://kilo.ai/docs/gateway/api-reference), and [model metadata](https://kilo.ai/docs/gateway/models-and-providers).
- [Codex configuration](https://developers.openai.com/codex/config-reference) and [advanced configuration](https://developers.openai.com/codex/config-advanced).
- [OpenCode models](https://opencode.ai/docs/models/) and [custom providers](https://opencode.ai/docs/providers/#custom-provider).
- [Claude Code gateways](https://code.claude.com/docs/en/llm-gateway-connect) and [model configuration](https://code.claude.com/docs/en/model-config).
- [Zed API providers](https://zed.dev/docs/ai/use-api-access#openai-compatible).
- [Cursor API keys](https://prod.cursor.com/help/models-and-usage/api-keys), [localhost limitation](https://forum.cursor.com/t/how-can-i-use-a-local-llm-on-my-desktop-ai-computer/152419), and [custom IDs](https://forum.cursor.com/t/add-custom-model-fail-no-models-available/163488).

Compatibility was investigated on September 7, 2026. Vendor behavior can change; the version-specific verification record is in [Codex compatibility notes](codex-desktop-compatibility.md).
