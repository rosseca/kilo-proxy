# Client setup

Start Kilo Local and use the control panel to generate configuration for your selected models. Merge JSON snippets into existing settings rather than replacing the entire file. The local key grants access to your organization’s credits while the proxy is running; do not commit generated credentials.

## Codex Desktop: a second GUI instance

The **Codex Desktop** tab launches the installed desktop application with separate configuration and interface data, allowing your normal Codex to remain open.

1. Select your models and click **1. Prepare Codex GUI**. The helper creates `~/.codex-kilo-desktop` (`%USERPROFILE%\.codex-kilo-desktop` on Windows) and saves both `config.toml` and `models.json` on this computer.
2. Choose the operating system and actual installed app path in the helper. On macOS this may be `/Applications/ChatGPT.app` or `/Applications/Codex.app`; select the bundle that contains Codex.
3. Copy and execute the generated launch command. It supplies `CODEX_HOME`, `CODEX_ELECTRON_USER_DATA_PATH`, `--user-data-dir`, and `KILO_LOCAL_API_KEY` to the new instance. macOS uses `open -n`.

Interface data lives in `~/Library/Application Support/Codex Kilo` on macOS, `%LOCALAPPDATA%\Codex Kilo` on Windows, or `${XDG_CONFIG_HOME:-~/.config}/codex-kilo-desktop` on Linux. Relaunching the same isolated profile may focus its existing window.

Keep the normal `~/.codex/config.toml` unchanged. Do not copy its `auth.json`, cookies, or history. The generated provider uses the local proxy key and `cli_auth_credentials_store = "file"`. Profiles isolate settings and history, not workspaces: use separate checkouts or worktrees for concurrent edits to the same repository. If you previously edited your main config, restore its original provider/model manually.

The Electron isolation variable is version-dependent rather than a stable public API. [Compatibility notes](codex-desktop-compatibility.md) describe the inspected macOS version and what was tested. The original vendor app is installed separately and is not redistributed here. Windows and Linux require verification with the installed app version.

### Multiple models, reasoning, and short names

1. Check models in the catalog, or select search results in bulk, up to 50. **Selected only** filters the same list. Manual IDs are under **Add by ID**.
2. Adjust reasoning in the selected row and choose **Use on startup** for the initial model. **Customize levels** changes that model’s available efforts; **Use suggested levels** restores known defaults.
3. Edit **Name in Codex** for a short display name such as Sol, GLM, or Fable. Clearing it restores the catalog label. Names are limited to 80 characters and searchable.
4. Click **1. Prepare Codex GUI**. The helper creates missing files and updates Kilo settings in an existing `config.toml`: initial model, reasoning, catalog path, local port, Responses protocol and environment-variable authentication. Other settings and comments are preserved; conflicting Kilo authentication settings are removed. A selected named profile is synchronized too.
5. Use **2. Copy launch command** and run it in a terminal. Close the Kilo instance first if it is already open. Repeat Prepare after changing models, reasoning or the local port; no manual TOML copying is needed.

**Load saved catalog** restores models, labels, and reasoning. Unsaved changes last only for the panel session. Saving updates both profile files without storing the local API key in TOML. Each changed existing file receives an exact `.bak` copy; saving unchanged files leaves backups intact. Invalid TOML and unsafe file destinations stop the save without replacing either profile file. The config references `model_catalog_json = "models.json"`; the exporter puts the initial model first because the inspected app-server’s `model/list` default follows catalog order.

Model IDs remain unchanged. Renaming changes only `display_name`; reasoning is sent as `reasoning.effort`. Explicit efforts from Kilo’s `opencode.variants` metadata take priority over exact-ID fallback presets. Manual customizations take priority over both. Variant names and token budgets are not interpreted as effort levels. Unknown models receive no invented levels and can be configured manually.

Current exact-ID presets include Sol discounted (none/low/medium/high/xhigh/max, initially low), GLM 5.3 and 5.3 Flash (low/high/max, initially max), and Fable 5.1 (low/medium/high/xhigh/max, initially high). These are metadata/preset choices, not guarantees that every gateway route accepts every effort.

All models in this profile must support **Responses**. Catalog listing does not translate protocols or verify credits. Exported capabilities are conservative and coding instructions are original generic instructions, not vendor system prompts.

The optional TOML template and catalog download remain available for another computer. Automatic preparation always targets the computer running Kilo Local, regardless of the launcher platform selected in the web panel.

### “Missing environment variable”

Copy `env_key = "KILO_LOCAL_API_KEY"` literally. It names an environment variable; never replace it with the actual token. Use **Prepare Codex GUI** first, then **Copy launch command** to pass the local key when opening Codex. Close a previously running Kilo instance before relaunching with updated environment values.

The on-screen launch preview masks the key and cannot be executed as displayed. The copy button uses the real key. **Show key in command** reveals the executable text for manual selection.

### Fable / Anthropic tool schemas

For `/v1/responses` requests to `anthropic/*` and `~anthropic/*`, the proxy adapts function schemas with root `oneOf`, `anyOf`, or `allOf`. The original schema is preserved under required `kilo_tool_input`; constraints are not removed or flattened. Namespaced tools are supported.

Historical call arguments are wrapped and new arguments are unwrapped before reaching Codex. For affected tools only, SSE argument fragments are buffered until `response.function_call_arguments.done` and emitted as one validated delta. Other events and keepalives continue streaming. Invalid or incomplete wrappers stop the response rather than send malformed calls.

Local JSON Pointer references are relocated. Schemas with IDs, anchors, external references, or dynamic references are rejected because they require additional resolution. Requests, adapted JSON responses, and individual SSE events have a 32 MiB limit. This is schema adaptation, not a Responses-to-Messages translator; protocol support still depends on Kilo.

## Codex CLI

The **Codex CLI** tab offers the same helper as Codex Desktop, with an independent model selection and profile:

1. Check models in the catalog, or add exact IDs manually. Set short display names, the initial model and supported reasoning levels in each row.
2. Click **Prepare Codex CLI**. Kilo Local creates `~/.codex-kilo-cli` (`%USERPROFILE%\.codex-kilo-cli` on Windows) and saves `config.toml` plus `models.json`. Existing Kilo settings are updated; unrelated settings and comments are retained, with exact `.bak` backups of changed files.
3. Copy the launch command and run it from your project directory. Install Codex CLI first so `codex` is on your terminal's PATH. The command requires both saved files and scopes the local key and `CODEX_HOME` to the Kilo session.
4. Use `/model` inside Codex CLI to select a model and its reasoning level. Restart the CLI session after changing the catalog in the helper. **Load saved catalog** restores this CLI profile's models, names and reasoning choices.

GUI and CLI selections, saved catalogs and readiness indicators are independent. Codex Desktop continues to use `~/.codex-kilo-desktop`; ordinary Codex continues to use its usual profile. Both Kilo integrations use HTTP Responses and the same model capability catalog format. Optional TOML and JSON downloads remain available for another computer. Preparation always writes on the computer running Kilo Local.

The generated CLI profile was checked against the Codex executable bundled with the installed desktop app: both selected models, short labels, the initial model and exact reasoning choices appeared in `model/list`, without inference. The separate npm CLI installation on the development machine could not be validated because its executable was missing, including after reinstalling the same version.

Reference: [OpenAI configuration reference: model_catalog_json](https://learn.chatgpt.com/docs/config-file/config-reference).

## OpenCode

Add multiple models in the **OpenCode** tab and choose the initial model. Merge the generated custom provider into your OpenCode configuration. Use `/connect`, choose **Other**, and supply the local key for the configured provider. Use `/models` to switch between configured models. This helper targets OpenCode v1 and the OpenAI-compatible Chat Completions path.

## Claude Code: automatic isolated setup

1. Open **Claude Code** in the helper. It detects your installed version and shows whether the Kilo configuration is supported. Use **Detect version** after updating Claude.
2. Check models directly in the catalog. Edit a short name, choose **Use on startup**, and set supported reasoning preferences in each row. Manual IDs, search, prices, bulk selection and **Selected only** work like the Codex Desktop picker.
3. Click **1. Prepare Claude Code**. This creates `~/.claude-kilo` (`%USERPROFILE%\.claude-kilo` on Windows), saves `settings.json` and `kilo-models.json`, and shows **Configuration saved**. Changed existing files receive exact `.bak` backups. Unrelated settings such as permissions and hooks are retained.
4. Copy and run **2. Copy launch command** in your project directory. It supplies `CLAUDE_CONFIG_DIR` and `--settings`, clears conflicting inherited authentication/provider variables for that child, and restores the parent environment. The normal Claude profile remains available in another terminal.
5. Use `/model` to switch models. Restart a Kilo session after preparing changes. **Load Claude profile** restores selected models, names, initial model, aliases and reasoning preferences.

The profile contains only the local proxy credential, not your Kilo account key. It configures `ANTHROPIC_BASE_URL` without a trailing `/v1`, uses bearer authentication, and maps internal Sonnet/Opus/Haiku aliases to selected models. Modern versions also pin the Fable default. Advanced alias assignments are optional. Existing Kilo routing/authentication/model mappings are refreshed; policy settings such as `availableModels` remain in force.

### Compared with Codex Desktop

| Feature | Claude Code with Kilo Local |
| --- | --- |
| Automatic profile creation, updates and backups | Supported on macOS, Linux and Windows |
| Multiple models and short native picker labels | `modelPicker` from Claude Code 2.1.242; earlier versions use three aliases and `/model ID` commands |
| Per-model persistent effort | `modelSettings` from 2.1.251, for recognized Claude models; earlier versions use a global initial effort |
| Arbitrary Codex reasoning levels | Not portable. Claude accepts its own model-dependent levels; `max` is session-only, and unknown/non-Claude models receive no invented effort options |
| Catalog prices and request inspection | Available in Kilo Local; Claude's own price calculations can differ |
| Spend by conversation | Uses `x-claude-code-session-id` when present; missing billing/session data remains unknown or unassigned |
| Independent running clients | Separate terminal sessions with the isolated Claude profile |
| Second desktop GUI instance | This launcher opens Claude Code in a terminal. It does not configure Claude Desktop |

The automatic mode matches the installed version. If preparing for a newer installation, select **Claude Code 2.1.251 or later** and update it before launching. Upgrade with `claude update`, then detect the version and prepare again. The helper reports configuration compatibility, not a successful paid gateway request.

Recognized Claude families use native model IDs in the picker plus `modelOverrides` to send the original Kilo ID to the gateway. This avoids gateway spellings such as `anthropic/claude-fable-5.1` losing native reasoning recognition. Choose one gateway spelling per native family/version. Per-model effort preferences are keyed by Claude's canonical model name. Fable 5, Opus 4.7 and Opus 4.8 can retain their first-use default until an explicit `/effort` choice; the helper documents that native behavior.

Models must support Anthropic Messages, tools and the capabilities Claude sends. Anthropic does not officially support non-Claude models through gateways; configuring an ID does not certify compatibility. The proxy forwards `/v1/messages?beta=true`, version/beta headers and streaming responses. Optional token-counting endpoints are not implemented; Claude can fall back. Organization policy and access restrictions still apply.

See [Claude Code compatibility verification](claude-code-compatibility.md).

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
