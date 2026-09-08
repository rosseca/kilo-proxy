# Xcode: Chat, Codex and Claude

Kilo Proxy offers three independent setups under **Xcode**. Chat uses the OpenAI-compatible Chat Completions API, Codex uses Responses, and Claude uses Anthropic Messages. These are request formats; a model appearing in the gateway catalog does not guarantee support for all three.

## Xcode Chat

1. Select **Chat**, check models in the catalog or add exact gateway IDs, and optionally enter short names. Use **Refresh catalog** to fetch models without leaving Xcode's helper.
2. Click **1. Prepare profile**. The selected list is saved in Kilo Proxy's configuration directory as `xcode-chat.json`. Changes receive a `.bak` backup.
3. Click **2. Copy Xcode connection**. In Xcode, open **Settings → Intelligence → Add a Chat Provider** (or **Add a Model Provider** in older versions). Choose **Internet Hosted** so you can enter an authentication header, even though this provider is local.
4. Use the copied URL, for example `http://127.0.0.1:8877/xcode`, header `Authorization`, and value `Bearer <local-key>`. Do not append `/v1`: Xcode adds it. The preview hides the key; the copy button includes the real local key.
5. Keep Kilo Proxy running, choose a model in Xcode and start a conversation. Refresh or re-add the provider if Xcode retains an older model list.

Xcode receives only this saved list from `/xcode/v1/models`; requests use `/xcode/v1/chat/completions`. The regular `/v1/models` endpoint is unchanged for other clients. The initial model is listed first, but Xcode controls its active choice. Short names are supplied as hints; Xcode may display exact IDs. The list is a discovery filter, not an authorization policy. Model IDs are never rewritten and requests to the dedicated Chat path preserve the usual upstream authentication and organization header.

Provider registration in Xcode remains manual. No undocumented preference or Keychain entries are edited.

## Codex in Xcode

1. Close Xcode before changing an agent profile. In the **Xcode** helper, select **Codex**.
2. Select models, names, an initial model and supported reasoning defaults. The catalog uses the reasoning enum verified in Xcode's advertised Codex 0.106.0: `none`, `minimal`, `low`, `medium`, `high` and `xhigh`, intersected with each model's capabilities. `max` and `ultra` are omitted; if a model's default is unavailable, the helper chooses `high` when supported, otherwise the first supported level. Other Codex profiles keep their existing options.
3. Click **1. Prepare profile**. On the Mac running Kilo Proxy, it creates or updates:
   - `~/Library/Developer/Xcode/CodingAssistant/codex/config.toml`
   - `~/Library/Developer/Xcode/CodingAssistant/codex/models.json`
4. Reopen Xcode, install or enable Codex in **Settings → Intelligence**, and start a new conversation. Keep the proxy running.

This is Xcode's own Codex profile, separate from ordinary Codex and both Kilo Desktop/CLI profiles. It affects Codex conversations launched by Xcode; it does not create another Xcode instance. Existing unrelated TOML settings and comments are retained. Changed files receive exact `.bak` backups.

Xcode does not inherit the Kilo CLI launcher's environment. Its provider therefore uses `experimental_bearer_token` containing only the local proxy key in a protected configuration file, with `requires_openai_auth = false`. It does not depend on `KILO_LOCAL_API_KEY` or copy an OpenAI login. Prepare again after changing the proxy port or local credential.

## Claude in Xcode

1. Close Xcode and select **Claude** in the **Xcode** helper.
2. Select models. The helper uses the Claude version advertised by the detected Xcode bundle, independently of the Claude Code installed in your terminal.
3. For older versions, choose up to three models and review their Sonnet, Opus and Haiku alias assignments. The initial model can have a global reasoning preference. Newer advertised versions can receive native picker names and per-model reasoning settings.
4. Click **1. Prepare profile** to update:
   - `~/Library/Developer/Xcode/CodingAssistant/ClaudeAgentConfig/settings.json`
   - `~/Library/Developer/Xcode/CodingAssistant/ClaudeAgentConfig/kilo-models.json`
5. Reopen Xcode, install or enable Claude in **Settings → Intelligence**, and start a new conversation with Kilo Proxy running.

The dedicated Xcode settings contain the local proxy URL and bearer key. Existing unrelated settings are retained with exact `.bak` backups. The regular Claude Code profile and `~/.claude-kilo` are not modified. Model aliases also affect the agent's internal tasks, so each selected model must support Messages.

## Compatibility and validation

Detection checks the selected Xcode installation and then standard `/Applications/Xcode*.app` locations. Displayed agent versions come from Xcode's `AgentVersions.plist`; downloaded agent updates may differ. The development installation is Xcode 26.4.1, advertising Codex 0.106.0 and Claude 2.1.59. The helper does not substitute the terminal Claude version or let the browser force modern settings on that older bundled version.

Xcode owns its model picker and can supply model overrides when starting an agent. Names, full model lists and reasoning controls in the agent configuration are not a promise that Xcode displays every option. Preparing a profile does not install agents, sign in to their products or prove a successful gateway request. Agent preparation is available only on macOS with Xcode; Chat selection can be prepared wherever the proxy runs.

Verified with automated tests: independent profile writes and backups, authentication without a terminal variable for Codex, older Claude alias settings, the dedicated authenticated model list, Chat request routing with the organization header, English/Spanish UI and mobile layout. The generated Codex profile was loaded by the installed desktop app's Codex executable using `model/list`, confirming exact IDs, names, initial model and reasoning levels without inference. No paid gateway request or live conversation inside Xcode was performed; Xcode's downloaded agents were not installed on the development machine.

Sources: [Apple: setting up coding intelligence](https://developer.apple.com/documentation/xcode/setting-up-coding-intelligence), [Apple: customizing agent environments](https://developer.apple.com/documentation/xcode/extending-and-customizing-agents), and [OpenAI configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference).

Codex compatibility source: [OpenAI Codex 0.106.0 reasoning enum](https://github.com/openai/codex/blob/rust-v0.106.0/codex-rs/protocol/src/openai_models.rs).
