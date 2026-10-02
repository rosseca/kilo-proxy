# Claude Desktop · Kilo

**Claude Desktop · Kilo** opens the official Claude Desktop application with a private Kilo profile. Its full-width card sits immediately below Codex on **Agents**, separately from Claude Code CLI. Install the official Claude Desktop application separately. Open ordinary Claude from its usual icon; it can stay open while you use the Kilo instance.

## Setup

1. Add at least one Claude model to the shared **Models** library. Keep the exact Kilo model ID; a short display name is optional.
2. If **Claude Desktop · Kilo** is already running, quit only that instance before opening it again or changing its profile. Ordinary Claude can remain open. Existing conversations are never closed automatically.
3. Click **Open Claude Desktop · Kilo**. Kilo Proxy prepares the private gateway profile, starts the proxy if needed, and opens the Kilo instance. **Options → Integration settings** also offers preparation without launching.

The configuration points to `http://127.0.0.1:<port>` using the local proxy key. Desktop adds `/v1/messages`; do not append `/v1` to this gateway base URL. Your Kilo API key and organization header stay in Kilo Proxy. No Anthropic account sign-in is needed for the configured third-party gateway.

Claude's Electron networking adds `Sec-Fetch-Site: none`, `Sec-Fetch-Mode: no-cors` and `Sec-Fetch-Dest: empty`, including on its credential check. The proxy accepts this exact combination only on `POST /v1/messages`, without an Origin header and with the exact local host and valid local key. Browser origins, cross-site requests and preflight remain rejected; no CORS access is enabled.

If you configure Desktop manually, its official editor is under **Developer → Configure Third-Party Inference** after enabling developer mode through **Help → Troubleshooting**. The helper writes the supported local configuration files, so it does not need to toggle developer mode itself.

## Models and limits

Desktop receives compatible Claude IDs and names from the shared library. The shared default is used if compatible; otherwise the first compatible Claude model becomes Desktop's default, with an explanation in the helper. Other models are omitted only from this agent's generated selection. The original library and its default are unchanged. An empty compatible selection stops preparation with guidance to add a Claude model.

Desktop validates the model family before sending inference. It rejects the real IDs of GPT, GLM and other non-Claude routes. By default, the helper includes only native Claude models. The optional experimental mode below uses local routing aliases to allow other providers without modifying the vendor application.

The inspected Desktop configuration supports a model name, a display label and certain Claude-specific capabilities; it has no general per-model context-window or output-token field. The helper therefore does not export the library's numeric token limits or invent supported reasoning levels. Desktop controls its own context and reasoning behavior.

### Experimental models from other providers

In **Claude Desktop · Kilo → Options → Integration settings**, enable **Experimental: use models from other providers**. The choice is saved in Kilo Proxy settings and applies to the next preparation/open. The native helper includes all real model IDs from the shared library, keeping its names, order and default. The browser helper has the same opt-in for its own selection.

Claude's configuration receives a stable internal alias for each non-Claude model and a display label showing the real model name. Kilo Proxy translates only the request's `model` to its actual Kilo ID and restores the internal alias in the client-facing JSON response or streamed `message_start`. Native Claude IDs keep their normal route. Prompts, tools, tool results, thinking blocks, caching controls and provider usage metadata are not renamed or stripped.

Aliases contain a SHA-256 digest encoded as decimal digits. This avoids incidental matches against Desktop's model-name denylist. Each alias always identifies the same real model; deleting a model from the saved selection makes that alias unavailable instead of routing an existing conversation to a different provider. Do not enter aliases manually in the shared library.

Activity and costs use the upstream provider's real model ID. Debug capture, when explicitly enabled, shows the original alias request and the translated upstream request separately. Turning experimental mode off immediately prevents new alias requests; existing native Claude requests are unaffected. Prepare the profile again to remove experimental entries from Desktop's picker.

This is compatibility mode, not vendor support for those models. Each Kilo route must support Anthropic Messages, streaming and the tools used by the task. A successful text response does not prove support for Cowork, server-side tools, images, all reasoning options or long contexts. The helper does not advertise Claude-specific capabilities or apply numeric context presets to aliased models. Unknown aliases may have no effort selector; gateway acceptance of a reasoning field does not guarantee that a provider honors it.

Desktop still supplies its own Claude identity instructions. An aliased model may therefore describe itself as Claude or quote its internal alias when asked what it is. The proxy preserves those prompts; use Kilo Proxy's upstream model and usage records to verify the actual route.

## Profile storage and restoration

From v0.53.0, the private profile lives under `claude-desktop/` inside Kilo Proxy's configuration directory:

| Data | Location within `claude-desktop/` |
| --- | --- |
| macOS Desktop data and third-party configuration | `ui-3p/` |
| Code settings and authentication | `code/` |
| Windows Desktop data and third-party configuration | `local-app-data/Claude-3p/` |
| Windows child process application-data roots | `local-app-data/` for `LOCALAPPDATA`; `app-data/` for `APPDATA` |

The launcher sets the Windows environment only for the Kilo child process. A custom Kilo Proxy `--config-dir` also relocates this profile. Selection preferences remain in `claude-desktop-models.json` at the Kilo Proxy configuration root, separately from the shared model library.

The helper maintains a **Kilo Proxy** entry in the private `configLibrary`, applies that entry, and selects third-party deployment mode. It does not copy ordinary Claude's history or login, or modify the global `~/Library/Application Support/Claude-3p/` on macOS or `%LOCALAPPDATA%\Claude-3p\` on Windows. The Kilo instance starts with its own Desktop data and Code/auth profile.

Unrelated configuration entries and settings within the private profile are preserved. Changed files receive backups, sensitive files are written with private permissions, and malformed or unsafe profile paths stop preparation. Managed inference policy takes precedence; the helper refuses to overwrite an administrator's inference configuration.

If an earlier Kilo Proxy release left ordinary Claude using Kilo's gateway, restore it once through Desktop's third-party configuration selector or its normal Claude sign-in option, then restart ordinary Claude. Do not delete its data folder. After that, use the usual Claude icon for ordinary Claude and **Open Claude Desktop · Kilo** for the private instance.

This separates application data; it is not an operating-system sandbox. Claude disables Chrome-extension pairing when its data directory is relocated. Cowork and application updates have not been verified with the isolated profile.

## Compatibility evidence

The isolated profile was checked with the official macOS application **2.9939.4** and a synthetic gateway. Desktop started with private Desktop data, Code configuration and secure storage, passed its `POST /v1/messages` credential probe, and exited cleanly. Ordinary Claude remained running, and the global third-party deployment mode, config-library metadata and Kilo entry were byte-for-byte unchanged. This check did not exercise Chat or Code through the isolated Desktop interface. Windows discovery and configuration paths have automated coverage; a real Windows Desktop session has not been tested. Linux does not have an official Claude Desktop distribution.

The following gateway and model acceptance runs used macOS Desktop **2.9939.2** with the earlier shared third-party profile and Anthropic's gateway/configuration documentation. They establish gateway and model behavior; they do not establish Chat or Code UI compatibility in the isolated profile.

Local acceptance testing on macOS verified the actual application: the credential probe passed, the picker displayed native Claude names, Chat returned a requested marker, and Code read a file in a disposable workspace with its Read tool and returned its exact contents. The picker also displayed the shared GPT, GLM, MiniMax and DeepSeek names after experimental mode was enabled.

With the experimental aliases, **GPT-6 Luna and GLM 5.3 Flash** both completed a real Code session using Read, then answered a follow-up from the full conversation history. GPT-6 Luna also returned the requested marker in Chat. After correcting the gateway's explicit `Content-Encoding: identity` response header, all 14 requests in this acceptance run returned HTTP 200 with complete usage and provider costs attributed to the real model. Desktop's own statistics may still abbreviate an internal alias even though its model picker shows the configured display name.

These checks used a separate proxy port and left the existing Kilo Proxy process running. Other entries appearing in the picker are not proof that every model supports every Desktop feature. Cowork VM workflows and the image MCP were not exercised by this acceptance test.

Additional live API calls through Kilo Proxy's `/v1/messages` route succeeded for Claude Haiku 4.5, GPT-4.1 Nano and GLM 5.3 Flash: JSON text, streamed text, streamed tool arguments and non-Claude tool-result continuations. GPT-4.1 Nano was checked through the API only; the Desktop acceptance tests above used GPT-6 Luna and GLM 5.3 Flash.

The alias route also passed two live API checks per model for **GPT-6 Astra, GPT-6 Sol, GLM 5.3, MiniMax M3 and DeepSeek 4.1 Flash**: a forced synthetic tool call, followed by a successful continuation with the complete assistant content and tool result. All ten calls returned HTTP 200 and preserved the expected client alias. These are API checks, not additional Desktop UI or Cowork acceptance tests.

GLM returned `end_turn` alongside complete tool calls. The proxy corrects that terminal reason to `tool_use` only after validating complete tool arguments and a complete response. Text, tool deltas, usage, cache fields and request bodies are preserved; incomplete or failed streams are not reclassified as successful tool calls.

References: [Gateway configuration](https://claude.com/docs/third-party/claude-desktop/gateway), [configuration reference](https://claude.com/docs/third-party/claude-desktop/configuration), [model configuration](https://claude.com/docs/third-party/claude-desktop/models), and [in-app setup](https://claude.com/docs/third-party/claude-desktop/in-app-configuration).
