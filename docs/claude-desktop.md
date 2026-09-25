# Claude Desktop

Claude Desktop is a separate integration from Claude Code CLI. Its full-width card sits immediately below Codex on **Agents**. Install the official Claude Desktop application separately.

## Setup

1. Add at least one Claude model to the shared **Models** library. Keep the exact Kilo model ID; a short display name is optional.
2. Quit Claude Desktop if it is already running. Existing conversations are never closed automatically.
3. Click **Open Claude Desktop**. Kilo Proxy prepares the gateway profile, starts the proxy if needed, and opens Desktop. **Options → Integration settings** also offers preparation without launching.

The configuration points to `http://127.0.0.1:<port>` using the local proxy key. Desktop adds `/v1/messages`; do not append `/v1` to this gateway base URL. Your Kilo API key and organization header stay in Kilo Proxy. No Anthropic account sign-in is needed for the configured third-party gateway.

Claude's Electron networking adds `Sec-Fetch-Site: none`, `Sec-Fetch-Mode: no-cors` and `Sec-Fetch-Dest: empty`, including on its credential check. The proxy accepts this exact combination only on `POST /v1/messages`, without an Origin header and with the exact local host and valid local key. Browser origins, cross-site requests and preflight remain rejected; no CORS access is enabled.

If you configure Desktop manually, its official editor is under **Developer → Configure Third-Party Inference** after enabling developer mode through **Help → Troubleshooting**. The helper writes the supported local configuration files, so it does not need to toggle developer mode itself.

## Models and limits

Desktop receives compatible Claude IDs and names from the shared library. The shared default is used if compatible; otherwise the first compatible Claude model becomes Desktop's default, with an explanation in the helper. Other models are omitted only from this agent's generated selection. The original library and its default are unchanged. An empty compatible selection stops preparation with guidance to add a Claude model.

Desktop validates the model family before sending inference. It rejects the real IDs of GPT, GLM and other non-Claude routes. By default, the helper includes only native Claude models. The optional experimental mode below uses local routing aliases to allow other providers without modifying the vendor application.

The inspected Desktop configuration supports a model name, a display label and certain Claude-specific capabilities; it has no general per-model context-window or output-token field. The helper therefore does not export the library's numeric token limits or invent supported reasoning levels. Desktop controls its own context and reasoning behavior.

### Experimental models from other providers

In **Claude Desktop → Options → Integration settings**, enable **Experimental: use models from other providers**. The choice is saved in Kilo Proxy settings and applies to the next preparation/open. The native helper now includes all real model IDs from the shared library, keeping its names, order and default. The browser helper has the same opt-in for its own selection.

Claude's configuration receives a stable internal alias for each non-Claude model and a display label showing the real model name. Kilo Proxy translates only the request's `model` to its actual Kilo ID and restores the internal alias in the client-facing JSON response or streamed `message_start`. Native Claude IDs keep their normal route. Prompts, tools, tool results, thinking blocks, caching controls and provider usage metadata are not renamed or stripped.

Aliases contain a SHA-256 digest encoded as decimal digits. This avoids incidental matches against Desktop's model-name denylist. Each alias always identifies the same real model; deleting a model from the saved selection makes that alias unavailable instead of routing an existing conversation to a different provider. Do not enter aliases manually in the shared library.

Activity and costs use the upstream provider's real model ID. Debug capture, when explicitly enabled, shows the original alias request and the translated upstream request separately. Turning experimental mode off immediately prevents new alias requests; existing native Claude requests are unaffected. Prepare the profile again to remove experimental entries from Desktop's picker.

This is compatibility mode, not vendor support for those models. Each Kilo route must support Anthropic Messages, streaming and the tools used by the task. A successful text response does not prove support for Cowork, server-side tools, images, all reasoning options or long contexts. The helper does not advertise Claude-specific capabilities or apply numeric context presets to aliased models. Unknown aliases may have no effort selector; gateway acceptance of a reasoning field does not guarantee that a provider honors it.

Desktop still supplies its own Claude identity instructions. An aliased model may therefore describe itself as Claude or quote its internal alias when asked what it is. The proxy preserves those prompts; use Kilo Proxy's upstream model and usage records to verify the actual route.

## Profile storage and restoration

On macOS, the third-party profile is stored under `~/Library/Application Support/Claude-3p/`; on Windows it uses `%LOCALAPPDATA%\Claude-3p\`. The helper maintains its own **Kilo Proxy** entry in `configLibrary`, sets that entry as applied, and selects third-party deployment mode. Selection preferences are stored in `claude-desktop-models.json` inside Kilo Proxy's configuration directory, separately from the shared model library.

Existing unrelated configuration entries and settings are preserved. Changed files receive backups, sensitive files are written with private permissions, and malformed or unsafe profile paths stop preparation. Managed inference policy takes precedence; the helper refuses to overwrite an administrator's inference configuration.

To return to another provider, use Desktop's third-party configuration selector or its normal Claude sign-in option and restart it. Do not delete its data folder. This integration does not claim simultaneous isolated Desktop instances: the vendor app shares its third-party configuration root and reads it at startup.

## Compatibility evidence

The implementation was checked against the official macOS application **2.9939.2** and Anthropic's gateway/configuration documentation. Windows discovery and configuration paths have automated coverage; a real Windows Desktop session has not been tested. Linux does not have an official Claude Desktop distribution.

Local acceptance testing on macOS verified the actual application: the credential probe passed, the picker displayed native Claude names, Chat returned a requested marker, and Code read a file in a disposable workspace with its Read tool and returned its exact contents. The picker also displayed the shared GPT, GLM, MiniMax and DeepSeek names after experimental mode was enabled.

With the experimental aliases, **GPT-6 Luna and GLM 5.3 Flash** both completed a real Code session using Read, then answered a follow-up from the full conversation history. GPT-6 Luna also returned the requested marker in Chat. After correcting the gateway's explicit `Content-Encoding: identity` response header, all 14 requests in this acceptance run returned HTTP 200 with complete usage and provider costs attributed to the real model. Desktop's own statistics may still abbreviate an internal alias even though its model picker shows the configured display name.

These checks used a separate proxy port and left the existing Kilo Proxy process running. Other entries appearing in the picker are not proof that every model supports every Desktop feature. Cowork VM workflows and the image MCP were not exercised by this acceptance test.

Additional live API calls through Kilo Proxy's `/v1/messages` route succeeded for Claude Haiku 4.5, GPT-4.1 Nano and GLM 5.3 Flash: JSON text, streamed text, streamed tool arguments and non-Claude tool-result continuations. GPT-4.1 Nano was checked through the API only; the Desktop acceptance tests above used GPT-6 Luna and GLM 5.3 Flash.

The alias route also passed two live API checks per model for **GPT-6 Astra, GPT-6 Sol, GLM 5.3, MiniMax M3 and DeepSeek 4.1 Flash**: a forced synthetic tool call, followed by a successful continuation with the complete assistant content and tool result. All ten calls returned HTTP 200 and preserved the expected client alias. These are API checks, not additional Desktop UI or Cowork acceptance tests.

GLM returned `end_turn` alongside complete tool calls. The proxy corrects that terminal reason to `tool_use` only after validating complete tool arguments and a complete response. Text, tool deltas, usage, cache fields and request bodies are preserved; incomplete or failed streams are not reclassified as successful tool calls.

References: [Gateway configuration](https://claude.com/docs/third-party/claude-desktop/gateway), [configuration reference](https://claude.com/docs/third-party/claude-desktop/configuration), [model configuration](https://claude.com/docs/third-party/claude-desktop/models), and [in-app setup](https://claude.com/docs/third-party/claude-desktop/in-app-configuration).
