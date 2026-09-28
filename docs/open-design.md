# Open Design

Run Open Design in **Local CLI** mode with **Codex CLI**, **Claude Code** or **OpenCode** connected to Kilo Proxy. Kilo Proxy prepares a private engine profile from your shared models, starts the local proxy, and opens a separate Open Design workspace on macOS or Windows. You do not need to copy a URL or API key into Open Design.

## Choose an engine and launch

1. Install [Open Design v0.22.2 or later](https://github.com/nexu-io/open-design/releases/latest) and your preferred CLI separately. Save your Kilo account and team connection, then choose your shared models in Kilo Proxy.
2. Open **Agents → Open Design → Engine settings**. Choose **Codex CLI**, **Claude Code** or **OpenCode** under **Engine**. If an installation is missing, install it and use **Options → Refresh detection** on the agent card.
3. Click **Launch Open Design**. Kilo Proxy prepares that engine's private profile and Open Design preferences, starts the saved proxy, and launches the installed app. A preparation or proxy startup error leaves the app closed and shows the problem in Kilo Proxy.
4. Complete Open Design's own first-run and privacy choices if prompted. Use **Local CLI** with the engine you selected and leave the model at **CLI default** to use your shared default. Create or open your project inside Open Design; the launcher does not hand off Kilo Proxy's project folder.
5. Try a request and check Kilo Proxy's **Activity** to verify the model and connection. Keep Kilo Proxy running while you work.

The generated profiles use the same configuration serializers as Kilo Proxy's existing CLI integrations. They are new private profiles: your normal CLI settings, the separate Kilo terminal profiles, and their saved sessions are not copied or changed. Open Design's ordinary workspace and settings also remain separate.

## Shared models, reasoning and file tools

With **CLI default** selected, the engine starts with the default from your shared **Models** library. Preparing the profile also applies the library's supported settings:

| Engine | Applied settings |
| --- | --- |
| Codex CLI | Exact model IDs, display names, model catalog, default and supported reasoning levels. Enabled image generation is exposed through the managed images MCP. |
| Claude Code | Exact gateway IDs and default, native model mappings, aliases and display names supported by the installed version. Reasoning is limited to supported Claude model families and CLI capabilities; unsupported levels are omitted. |
| OpenCode | Exact model IDs, names, default and supported context/output limits through the local OpenAI-compatible provider. Reasoning remains controlled by OpenCode. Enabled image generation is exposed through the managed images MCP. |

Open Design has its own model picker. The generated Claude model list does not automatically populate every entry in that picker. Keep **CLI default** for the shared default, or enter a supported exact model ID in Open Design when choosing a different model. A shared display name is not an API model ID. Explicit model or reasoning choices inside Open Design can override the underlying CLI defaults.

The selected model must support the engine's API protocol and be accessible to your organization: **Responses** for Codex CLI, **Anthropic Messages** for Claude Code, or **Chat Completions** for OpenCode. Local CLI exposes the engine's project tools, subject to its permissions and Open Design's capabilities. Open Design's direct API-provider/BYOK mode is a separate workflow; this integration configures Local CLI.

## Generate images with Codex CLI

Enable **Image generation** in Kilo Proxy's Codex image settings, then launch Open Design with **Codex CLI**. Its private profile exposes `kilo_images.generate_image`. Open Design 0.22.2 starts Codex with `approvalPolicy: "never"`, so an image call that requires an approval prompt can otherwise fail with `MCP tool call requires approval, but approval policy is never` before reaching Kilo Proxy.

When no explicit image-tool or server approval policy exists, preparation adds `approval_mode = "approve"` for **only** `mcp_servers.kilo_images.tools.generate_image` in the private Open Design profile. This permits image requests from that workspace without a separate Codex prompt; generation uses your selected image provider: Kilo credit or ChatGPT subscription quota. Global approvals, sandbox settings, unrelated MCP tools, and ordinary Codex Desktop/CLI profiles keep their existing permissions. Custom image-tool/server approval policies and enabled/disabled tool lists are preserved. An explicit policy requiring a prompt can still prevent noninteractive image calls. See Codex's [per-tool approval configuration](https://learn.chatgpt.com/docs/config-file/config-reference).

After updating Kilo Proxy, quit the managed Open Design instance and launch it again from Kilo Proxy to refresh the profile. Disabling image generation removes the managed images server, including its scoped approval, on the next preparation. A successful MCP connection alone does not confirm generation: ask for an image and inspect the tool result and Kilo Proxy's Activity for permission, model-access, or credit errors.

## Generate images with OpenCode

OpenCode can use the same Kilo image-generation MCP as Codex. Enable **Image generation** and save your provider choice in Kilo Proxy's Codex image settings (**Kilo** with an image-output model, or **ChatGPT subscription · Experimental**), then quit the managed Open Design instance and launch it again with **OpenCode**. These image settings are shared by Kilo Proxy; the coding model remains independent.

Preparation adds the `kilo_images` MCP server to Open Design's private OpenCode profile. Ask the agent to use its `generate_image` tool, for example: “Use kilo_images to generate a hero illustration, then copy the returned original file into this project.” The tool returns generated file paths and a preview; the agent can copy the original into the project using its file tools. Editing accepts a path returned by an earlier Kilo image-generation call. This does not configure Open Design's separate image-provider picker or video/audio providers.

The remote MCP uses the existing `http://127.0.0.1:<port>/mcp/images` endpoint, local bearer authentication, disabled OAuth, and a six-minute timeout. Image usage appears in Kilo Proxy's Activity. Disabling image generation removes only Kilo's managed MCP entry on the next preparation; other servers and tool permissions are preserved.

Open Design injects its own MCP servers through `OPENCODE_CONFIG_CONTENT`; the Kilo launcher preserves that configuration. [OpenCode merges the configurations](https://opencode.ai/docs/config/#precedence-order), so servers with different names coexist. Avoid defining another external server named `kilo_images` in Open Design: its higher-priority inline settings would override the managed entry. A conflicting same-name server in the private file stops preparation instead of being overwritten. Existing tool permissions still apply. The connection follows OpenCode's documented [remote MCP configuration](https://opencode.ai/docs/mcp-servers/#remote).

## Apply changes and reopen

Shared model edits, a different engine, or a changed local connection are applied when you next launch from Kilo Proxy. If the managed Open Design instance is already running and its configuration needs updating, quit that instance and click **Launch Open Design** again. Closing a window may leave its background process running; use Open Design's quit action. Kilo Proxy checks whether the managed instance is closed before writing these settings.

If you changed that workspace to API-provider mode inside Open Design, switch it back to **Local CLI** there. Mode is stored by the renderer, and the launcher does not overwrite its browser storage. A launch that only focuses an existing instance cannot replace that process's environment.

Update the installed Open Design app separately using its normal installation. Automatic updates are disabled in Kilo's managed namespace because upstream update payloads belong to Open Design's standard release namespace.

## Private files and credentials

Files live under `open-design/` in Kilo Proxy's [application configuration directory](shared-models.md#saved-files):

| Location under `open-design/` | Purpose |
| --- | --- |
| `profiles/codex-cli/` | Codex `config.toml`, `models.json` and this engine's private sessions |
| `profiles/claude/` | Claude `settings.json`, `kilo-models.json` and this engine's private state |
| `profiles/opencode/` | OpenCode `opencode.json`, `kilo-models.json` and the native launcher plus its descriptor |
| `namespaces/kilo-proxy-<id>/data/app-config.json` | Open Design's selected Local CLI engine, model default and per-engine environment preferences |
| `namespaces/kilo-proxy-<id>/` | The separate Open Design data, renderer storage, runtime and logs |
| `selection.json` | The prepared model selection and readiness hashes; no API credentials |

The namespace ID is derived from Kilo Proxy's configuration-directory path. Launching the same installed Open Design app normally continues to use its ordinary namespace.

Codex receives the local proxy key through `KILO_LOCAL_API_KEY`; its generated provider and images MCP refer to that variable without embedding the key. Its private Open Design environment preferences also set `CODEX_API_KEY` to the local proxy key so Open Design recognizes API authentication without requesting a ChatGPT login. Claude's private settings and Open Design's Claude environment preferences contain the local proxy key; OpenCode's private provider settings and enabled image MCP headers also contain it. Profile files use the existing private-write and backup handling. Your upstream personal Kilo credential is not exported to these profiles or launch arguments.

OpenCode is launched through a private native Kilo Proxy executable that sets `OPENCODE_CONFIG` to this profile and then starts the detected OpenCode binary. This works on macOS and Windows without a shell script; Windows requires a native OpenCode `.exe` installation. The launcher forwards Open Design's arguments and process streams to the CLI.

## Desktop detection and Linux

Kilo Proxy detects these installed application locations:

| Platform | Supported application location |
| --- | --- |
| macOS | `/Applications/Open Design.app`, then `~/Applications/Open Design.app` |
| Windows | `%LOCALAPPDATA%\Programs\Open Design\Open Design.exe` |
| Linux | Automatic packaged-desktop preparation and launch are unavailable. |

The chosen CLI must also be detected. Kilo Proxy checks the packaged Open Design version and requires **v0.22.2 or later** for this integration; update an older installation before launching it through Kilo Proxy. Open Design hosts the CLI process itself, so this launch does not open an interactive terminal. The launcher does not install either application or discover arbitrary Open Design installations through a command named `od`.

For Codex, this workflow uses the native CLI executable. If discovery finds an npm or script wrapper, Kilo Proxy checks for a matching native executable bundled in Codex Desktop on macOS, then in the npm installation. A wrapper with a missing native package shows a repair/install error. This check does not change ordinary terminal CLI discovery.

Open Design v0.22.2 has no official prebuilt Linux desktop artifact. Follow its [run-from-source instructions](https://github.com/nexu-io/open-design/tree/73953213a6fec2c8092e8e77d229a3074aa828a9#-run-from-source) and configure that source build separately using its Local CLI settings. Kilo Proxy's [terminal commands](terminal-commands.md) provide `kilo-codex` and `kilo-claude` on Linux for running the configured agents in your current terminal; the packaged Open Design launcher does not automatically configure a source build.

Open Design's daemon and Kilo Proxy must run on the same host for the generated loopback URLs. A remote instance or a container with a separate network namespace cannot reach your computer's proxy through its own `127.0.0.1`.

## Compatibility and verification

The integration contract was checked against Open Design **v0.22.2**, commit [`73953213a6fec2c8092e8e77d229a3074aa828a9`](https://github.com/nexu-io/open-design/tree/73953213a6fec2c8092e8e77d229a3074aa828a9): its [packaged namespace configuration](https://github.com/nexu-io/open-design/blob/73953213a6fec2c8092e8e77d229a3074aa828a9/apps/packaged/src/config.ts#L118), [private paths](https://github.com/nexu-io/open-design/blob/73953213a6fec2c8092e8e77d229a3074aa828a9/apps/packaged/src/paths.ts), [Local CLI preference allowlist](https://github.com/nexu-io/open-design/blob/73953213a6fec2c8092e8e77d229a3074aa828a9/apps/daemon/src/app-config.ts#L205), [daemon environment forwarding](https://github.com/nexu-io/open-design/blob/73953213a6fec2c8092e8e77d229a3074aa828a9/apps/packaged/src/sidecars.ts#L90), [CLI model defaults](https://github.com/nexu-io/open-design/blob/73953213a6fec2c8092e8e77d229a3074aa828a9/apps/daemon/src/runtimes/models.ts#L197), and [renderer defaults](https://github.com/nexu-io/open-design/blob/73953213a6fec2c8092e8e77d229a3074aa828a9/apps/web/src/state/config.ts#L78).

Automated tests use temporary profiles, synthetic keys and simulated launch/runtime results. They verify profile generation, preserved custom settings, private namespaces, stale-setting and running-instance checks, and credential handling. A successful launch does not prove model access or billing; verify a real request through Open Design and Kilo Proxy's Activity.
