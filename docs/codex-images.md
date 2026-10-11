# Images through Kilo Proxy

Kilo Proxy can expose an optional `generate_image` MCP tool to the isolated **Codex Desktop** and **Codex CLI** profiles. Your coding model stays selected in the normal model picker. The image tool uses its own provider setting: a separate image model from the **Kilo** catalog, or **ChatGPT subscription · Experimental** and its built-in image tool. This choice is independent of the model and connection used for conversation.

The Kilo image tool is available in **v0.23.0 and later**. The ChatGPT provider is an experimental addition.

## Enable the tool

1. Connect the account you want to use for images: Kilo credentials and organization, or [ChatGPT subscription sign-in](chatgpt-subscription.md).
2. Choose coding models in the native **Models** library. Open **Models → Image generation**.
3. Enable **Image generation** and choose an **Image provider**. With **Kilo · account credits**, choose an **Image model** whose catalog metadata advertises image output; image input alone does not qualify, and coding-tool support is not required. With **ChatGPT subscription · Experimental**, the built-in image tool is selected automatically: no image model or separate OpenAI API key is required.
4. Click **Save image settings**. This saves the shared image choice without requiring a coding-model selection, a Codex profile, or a connected image account. Connect the chosen account before using the tool.
5. On **Agents**, use **Open Codex** for Desktop or **Open Codex CLI** for a terminal. This prepares the selected profile and applies the saved image settings. Manual **Prepare without opening** is under that agent's **Options → Integration settings**. Restart an already open Codex instance so it reloads the MCP configuration.
6. Ask Codex to use `generate_image`, for example: “Use the Kilo image tool to create a small illustration of a lighthouse at sunset.”

Keep Kilo Proxy running while using the tool. **Kilo** image requests use the configured organization and its gateway/provider billing, including BYOK; choosing a catalog model does not verify access, balance or editing support. **ChatGPT** image requests use the connected subscription and its quota, subject to account eligibility and backend availability. They do not use Kilo credit or the separately billed OpenAI API. Requests never fall back to the other provider if generation fails or quota is exhausted.

The image setting belongs to Kilo Proxy and is shared by agents using its image MCP, including Codex, OMP and supported Open Design engines. **Save image settings** persists it separately from the automatically saved coding-model library. Opening/preparing a Codex profile also saves any pending image draft. The optional browser helper retains its image controls inside the Codex tabs. Preparing another Codex profile applies the current setting there too. Changing the coding model does not change the image provider. Switching image providers preserves the saved Kilo image-model choice so you can switch back. Both account connections may remain active.

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

`generate_image` accepts a text `prompt` and an optional `reference_image` path. It saves the full-resolution original under Kilo Proxy's `generated-images` directory inside its application data directory and returns its absolute path.

From v0.23.1, the inline MCP image is a preview bounded to **1024 pixels on each side and 256 KiB of encoded image bytes per image**. Base64 encoding and the rest of the MCP response add to the transmitted size. The preview reduces the image data carried into later model requests; it does not replace or reduce the saved original.

Previews preserve aspect ratio. Small PNG/JPEG images that already fit are returned unchanged; larger images are resized or re-encoded, with PNG transparency retained. In the result, `images[].path`, `mimeType`, `width`, and `height` describe the original file. `images[].preview` describes the inline image with its own `mimeType`, `width`, `height`, `bytes`, and `resized` fields. If a preview cannot be created safely, `preview.omitted` is `true` and the result explains that the original remains available at its saved path. Generation is not repeated to recover a preview.

For an edit, pass a path returned by a previous successful generation. References are restricted to Kilo Proxy's own generated image files. Arbitrary local files and remote URLs are not accepted as references. For Kilo, the selected image model must also support image input for editing to succeed. For ChatGPT, edits use the built-in tool with the saved original as image input. You can ask Codex to copy a finished image into your project after generation.

## Payload limits and 413 errors

An **HTTP 413** means a request or response exceeded a body-size limit. The proxy's local upload allowance does not override limits enforced by Kilo, its hosting platform, or the model provider. For a Vercel-hosted route, Vercel documents a **4.5 MB** function request/response limit and the `FUNCTION_PAYLOAD_TOO_LARGE` error. See [Vercel's request body limits](https://vercel.com/docs/functions/limitations#request-body-size).

Codex can send previous tool results again as conversation context. A history created before v0.23.1 may already contain full-size base64 images; updating Kilo Proxy does not rewrite that history. Multiple previews, other attachments, and text can also exceed an upstream limit. Bounded previews reduce new image payloads; they do not guarantee that every conversation fits.

**Settings → Large images** offers local compression or image URLs for oversized requests. **Compress locally** uses a fixed **High quality**, **Balanced**, or **Small size** profile on outbound copies. **Cloudflare quick tunnel** and **Tailscale Funnel** serve original bytes from a dedicated temporary image server; **Litterbox** uploads them to third-party temporary storage with a selected expiry; **Upload to Kilo · Experimental** uses Kilo's Cloud Agent storage outside a documented Gateway integration. Fresh profiles default to **Cloudflare quick tunnel**; existing saved choices, including **Off**, are preserved. If `cloudflared` is missing, startup and Large images settings show installation guidance. Local originals remain untouched, and a selected mode never falls back to another service or compression profile. This also applies to Chat Completions and Anthropic Messages clients using Kilo Proxy. Read [setup, limits, and image lifetime](image-uploads.md) before choosing a mode.

If a conversation receives a 413, explicitly compact its history using the client's supported controls, start a new conversation, or reduce its attachments. If compaction itself cannot send the existing history, start a new conversation with the necessary text summary and saved image paths. Avoid attaching the full original again merely to export it: copy the saved file into your project. Kilo Proxy does not silently delete context or automatically retry a rejected request.

For an upstream 413 carrying the recognized `FUNCTION_PAYLOAD_TOO_LARGE` code, the proxy provides a clearer JSON diagnostic with `error.code = "upstream_payload_too_large"`, the outbound request size when known, and recovery guidance. The diagnostic identifies this as a transport-body limit rather than a model context-window limit, suggests local image compression for image-heavy requests, and warns that compression may not suffice. Other 413 responses pass through unchanged. Activity preserves the original rejection in **Gateway response** and the diagnostic in **Client response**. A smaller inline preview applies to new image results, not images already stored in the client's history. See [diagnostic recognition limits](security-and-debugging.md#local-access-and-upstream-requests).

## Activity and cost

Image usage counts toward the current Kilo Proxy session regardless of request capture. Kilo image requests record supported reported costs; ChatGPT image requests are recorded as subscription usage, without inventing a dollar price or marking subscription quota as missing Kilo billing. When the user enables request capture in Activity (off by default), image calls also appear in the recent-request inspector. Captured details include the prompt and sanitized headers, with authentication values hidden. Turning capture off clears these details, including pending captures. Base64 image payloads are omitted from traces. Reported usage and cost are extracted separately from the image data so a large image does not hide its billing information; an absent cost remains unknown rather than being counted as free.

ChatGPT MCP results identify `provider: "chatgpt"` and `billing: "subscription"`, and omit monetary amounts. Kilo results include `costUSD` and `costSource` when a supported cost is reported. The reader prefers `usage.cost_microdollars`, then `usage.cost_details.upstream_inference_cost`, then `provider_metadata.gateway.marketCost`, then `usage.cost`. These values are alternatives, never added together for one request. For example, a BYOK response with `usage.cost = 0` and an upstream inference cost of `0.21976` records **$0.21976** once. That provider cost can differ from the organization's Kilo charge. Activity retains only the relevant gateway `marketCost` from provider metadata, omitting unrelated metadata and image payloads. See [accounting fields and limits](security-and-debugging.md#passive-spend-tracking-0130).

Conversation attribution depends on a recognized session header reaching the MCP request. Without one, the image call remains unassigned to a conversation while still counting toward the Kilo Proxy session. The installed Codex probe verifies tool discovery and invocation, not automatic conversation attribution.

## Disable or troubleshoot

- To disable the feature, clear its toggle and click **Save image settings**. Reopening/preparing an agent removes the MCP entry managed by Kilo Proxy from its profile. Restart Codex to refresh its tool list.
- With **ChatGPT**, sign in through **Settings → Connection** before preparing the image tool. The provider does not require an image-model catalog entry. If ChatGPT is disconnected, the saved provider choice remains visible so you can reconnect or disable images.
- With **Kilo**, if no image model is listed, refresh the catalog. A coding model or a model that only accepts images as input cannot be selected for this tool.
- If a saved model no longer appears in the image catalog, the helper keeps the saved ID visible as unavailable. Choose a listed image model or disable the feature before preparing again.
- If Codex cannot connect to `kilo_images`, confirm Kilo Proxy is running and reopen Codex through its **Open** action on Agents so it receives the local-key environment variable. Changing the proxy port also requires preparing the profile again.
- If a generation times out, check its result before asking for another attempt: the upstream request may already have been billed.
- A Kilo error can reflect model availability, organization access, balance or unsupported editing. A ChatGPT error can reflect account eligibility, expired authorization, quota or subscription-backend changes. The proxy does not switch accounts or retry a billed generation automatically. The image tool does not replace Codex's built-in image feature or guarantee that a coding model will choose to call it.

## Validation

The browser E2E tests verify provider switching, saved Kilo-model preservation, disconnected-account guidance, independent settings persistence, and generated profiles. They use the real Go backend, temporary profiles, an in-memory credential store, and a synthetic Kilo endpoint that returns a valid tiny PNG and usage. They make no paid image requests and do not launch installed clients. Installed Codex compatibility is checked separately against a disposable app-server profile when that binary is available; discovering a tool is distinct from completing a live paid generation.

To run the focused checks from a development checkout:

```sh
npm run test:e2e -- e2e/images.spec.mjs
python3 scripts/check-codex-images.py /absolute/path/to/codex
```

The installed-binary probe was verified locally with Codex CLI **0.153.4** bundled in the desktop application. It creates an ephemeral thread, discovers `kilo_images` through `mcpServerStatus/list`, and invokes `generate_image` through `mcpServer/tool/call` against the real Go MCP and a synthetic Kilo gateway. It verifies the generated PNG bytes without sending `turn/start` or requesting model inference. These methods are documented in the [official app-server reference](https://learn.chatgpt.com/docs/app-server).

Live ChatGPT validation on September 28, 2026 completed generation and editing through the actual local MCP, using a separately authorized subscription. The edit reused the full-resolution original from the first call. Both returned local originals and bounded previews; the originals were visually checked, and Activity recorded two subscription requests without monetary charges or incomplete usage. This confirms the tested account and backend at that time, not permanent availability for every account.
