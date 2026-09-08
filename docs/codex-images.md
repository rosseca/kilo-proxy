# Images in Codex through Kilo

Kilo Proxy can expose an optional `generate_image` MCP tool to the isolated **Codex Desktop** and **Codex CLI** profiles. Your coding model stays selected in the normal model picker. The image tool uses a separate image model from the Kilo catalog and sends its requests through Kilo with your configured account and organization.

Available in Kilo Proxy **v0.23.0 and later**.

## Enable the tool

1. Connect Kilo Proxy with your Kilo credentials and organization.
2. Open the Codex Desktop or Codex CLI helper and choose your coding models as usual.
3. Enable **Image generation** and choose an **Image model**. The image picker includes models whose catalog metadata advertises image output; image input alone does not qualify. The image model does not have to support coding tools.
4. Use **Launch** to prepare the current profile and open Codex, or use **Prepare** to save it first. Restart an already open Codex instance so it reloads the MCP configuration.
5. Ask Codex to use `generate_image`, for example: “Use the Kilo image tool to create a small illustration of a lighthouse at sunset.”

Keep Kilo Proxy running while using the tool. Choosing a catalog model does not verify your organization's access, available balance, or that model's support for a particular generation request. Requests go through Kilo with the configured organization; charges depend on its gateway and provider billing setup, including BYOK. The tool does not call the OpenAI image API directly or require a separate OpenAI API key.

The image setting belongs to Kilo Proxy and is shared by its Codex helpers. Preparing another Codex profile applies the current setting there too. Changing the coding model does not change the selected image model.

## What is configured

The helper maintains one local MCP entry in the isolated profile's `config.toml`:

```toml
[mcp_servers.kilo_images]
url = "http://127.0.0.1:8877/mcp/images"
bearer_token_env_var = "KILO_LOCAL_API_KEY"
startup_timeout_sec = 15
tool_timeout_sec = 360
enabled = true
```

The port follows your local proxy setting. `KILO_LOCAL_API_KEY` is the name of the environment variable supplied by Kilo Proxy's launcher, not a place to paste a key. The server runs inside the Go application; no Node.js, Python, extra proxy, or separate MCP process is required by the feature. Codex supports HTTP MCP servers with a bearer-token environment variable and configurable tool timeouts; see the [official Codex MCP documentation](https://learn.chatgpt.com/docs/extend/mcp?surface=cli).

The helper preserves unrelated settings and MCP entries and creates profile backups when it changes existing files. If an unrelated MCP server already uses the name `kilo_images`, preparation reports a conflict instead of replacing it.

## Generated files and editing

`generate_image` accepts a text `prompt` and an optional `reference_image` path. It returns image content and the absolute path of the file saved under Kilo Proxy's `generated-images` directory inside its application data directory.

For an edit, pass a path returned by a previous successful generation. References are restricted to Kilo Proxy's own generated image files. Arbitrary local files and remote URLs are not accepted as references. The selected image model must also support image input for editing to succeed. You can ask Codex to copy a finished image into your project after generation.

## Activity and cost

Image calls appear in the current Kilo Proxy session's activity. Captured details include the prompt and sanitized headers, with authentication values hidden. Base64 image payloads are omitted from traces. Reported usage and cost are extracted separately from the image data so a large image does not hide its billing information; an absent cost remains unknown rather than being counted as free.

The MCP result includes `costUSD` and `costSource` when a supported cost is reported. The reader prefers `usage.cost_microdollars`, then `usage.cost_details.upstream_inference_cost`, then `provider_metadata.gateway.marketCost`, then `usage.cost`. These values are alternatives, never added together for one request. For example, a BYOK response with `usage.cost = 0` and an upstream inference cost of `0.21976` records **$0.21976** once. That provider cost can differ from the organization's Kilo charge. Activity retains only the relevant gateway `marketCost` from provider metadata, omitting unrelated metadata and image payloads. See [accounting fields and limits](security-and-debugging.md#passive-spend-tracking-0130).

Conversation attribution depends on a recognized session header reaching the MCP request. Without one, the image call remains unassigned to a conversation while still counting toward the Kilo Proxy session. The installed Codex probe verifies tool discovery and invocation, not automatic conversation attribution.

## Disable or troubleshoot

- To disable the feature, clear its toggle and prepare the profile again. This removes the MCP entry managed by Kilo Proxy from that profile and disables the shared image setting. Restart Codex to refresh its tool list.
- If no image model is listed, refresh the catalog. A coding model or a model that only accepts images as input cannot be selected for this tool.
- If a saved model no longer appears in the image catalog, the helper keeps the saved ID visible as unavailable. Choose a listed image model or disable the feature before preparing again.
- If Codex cannot connect to `kilo_images`, confirm Kilo Proxy is running and reopen Codex through **Launch** so it receives the local-key environment variable. Changing the proxy port also requires preparing the profile again.
- If a generation times out, check its result before asking for another attempt: the upstream request may already have been billed.
- A generation error from Kilo can reflect model availability, organization access, balance, or unsupported editing. The image tool does not replace Codex's built-in image feature or guarantee that a coding model will choose to call it.

## Validation

The browser E2E tests use the real Go backend, temporary profiles, an in-memory credential store, and a synthetic Kilo endpoint that returns a valid tiny PNG and usage. They make no paid image requests and do not launch installed clients. Installed Codex compatibility is checked separately against a disposable app-server profile when that binary is available; discovering a tool is distinct from completing a live paid generation.

To run the focused checks from a development checkout:

```sh
npm run test:e2e -- e2e/images.spec.mjs
python3 scripts/check-codex-images.py /absolute/path/to/codex
```

The installed-binary probe was verified locally with Codex CLI **0.153.4** bundled in the desktop application. It creates an ephemeral thread, discovers `kilo_images` through `mcpServerStatus/list`, and invokes `generate_image` through `mcpServer/tool/call` against the real Go MCP and a synthetic Kilo gateway. It verifies the generated PNG bytes without sending `turn/start` or requesting model inference. These methods are documented in the [official app-server reference](https://learn.chatgpt.com/docs/app-server).
