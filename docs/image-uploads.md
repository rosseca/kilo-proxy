# Large images: compression and temporary URLs

**Settings → Large images** controls oversized image requests across **Responses**, **Chat Completions**, and **Anthropic Messages**. Choose one mode:

| Mode | Behavior | Additional requirements |
| --- | --- | --- |
| **Off** | Send image data unchanged. Oversized requests can still fail with HTTP 413. | None |
| **Compress locally** | Optimize outbound copies using the selected quality profile. | None |
| **Upload to Kilo · Experimental** | Upload original bytes to Kilo's Cloud Agent attachment storage and request deletion after inference. | Your configured Kilo account |
| **Cloudflare quick tunnel** (default) | Serve original bytes from a temporary local image server through a public Cloudflare URL. | Installed `cloudflared`; no Cloudflare account or domain required |
| **Litterbox · Experimental** | Upload original bytes anonymously to temporary third-party storage. | Internet access; no account or extra executable; live availability has not been confirmed |
| **Tailscale Funnel** | Serve original bytes from a temporary local image server through a public Tailscale URL. | Installed Tailscale CLI, signed-in account, and Funnel enabled |

New profiles and settings without a saved image mode default to **Cloudflare quick tunnel**. Existing explicit selections, including **Off**, are preserved when upgrading.

The selected mode, compression profile, and Litterbox expiry save automatically in `settings.json` as `imageTransport.mode`, `imageTransport.profile`, and `imageTransport.litterboxTTL`. They apply to new requests without restarting. The optional browser helper exposes the same choices. Switching modes does not cancel cleanup for earlier requests.

**Only the selected mode is used: a failure never falls back to another service or to a different compression profile.** Requests at or below **4,400,000 bytes** pass through unchanged. This budget leaves room below the Gateway's 4.5 MB body limit.

Only recognized inline PNG, JPEG, GIF, and WebP image parts are eligible. This includes Responses `input_image` parts in messages and `function_call_output` arrays, Chat Completions `image_url` parts in message content, and Anthropic base64 image blocks in messages and `tool_result` content. Text, arbitrary base64 strings, tool arguments, file attachments, local paths, existing remote image URLs, and unknown content structures are not searched or rewritten. Invalid data in a recognized image part produces a validation error before publication.

Local original files, newly generated originals, and the client's saved conversation are unchanged in every mode. URL modes preserve the bytes received from the client, including dimensions and transparency. They cannot recover a full-resolution original from an MCP preview or another image that the client has already resized. A later request containing the same inline images may need to publish them again.

## Local compression

Compression first tries lossless optimization. If the body remains above the budget, it uses the selected fixed profile:

| Profile | Maximum longest side | JPEG quality |
| --- | --- | --- |
| **High quality** (default profile) | 3072 px | 92 |
| **Balanced** | 2048 px | 85 |
| **Small size** | 1280 px | 75 |

Aspect ratio is preserved. These are encoder settings, not a guarantee of a specific perceptual quality or final byte size. Static PNG images are first optimized without changing their pixels. Opaque static PNG, JPEG, and WebP images can use JPEG compression with the selected profile. Images with transparency remain PNG instead of being flattened. GIF images and animated PNG/WebP images pass through unchanged so animation frames are not discarded.

JPEG orientation metadata is retained. PNG color and orientation metadata are preserved during lossless optimization. PNG and WebP images with EXIF metadata are excluded from lossy conversion to avoid changing their orientation. To bound local decoding, compression accepts at most **40 million pixels per image**, **32,768 pixels per side**, and **80 million unique pixels per request**.

If the selected profile still cannot make the body fit, the proxy returns an explanation before inference. Choose another profile or a URL mode explicitly, reduce attachments, or compact the conversation. Compression does not create a remote attachment, but the resulting inline image still goes to Kilo and the model provider with the inference request.

## Temporary URL limits and request lifetime

For an authenticated request above the budget, the proxy validates eligible images and checks whether replacing them with links can make the body fit **before publishing anything**. It selects only as many images as needed, prioritizing the greatest reduction in request size. Identical image bytes within one request share one published image.

- Each image is limited to **20 MiB**. Cloudflare, Litterbox, and Tailscale support at most **64 unique published images per request**; Kilo storage supports at most **five**. Additional images may remain inline if the final body fits.
- The **32 MiB local request-body limit** still applies before base64 images are replaced. URL transport cannot receive arbitrarily large conversations.
- Requests that publish images have a **10-minute total timeout**, covering publication and inference. A shorter image-link expiry can shorten that deadline.
- At most **two large-image requests** can be active at once. Concurrent requests using the same tunnel backend share its image server, with separate image URLs and cleanup.
- Oversized Responses requests using `background: true` are rejected before publication. Temporary image lifetime must cover the actual inference; use a foreground request or local compression.
- Provider format, resolution, context, and account restrictions still apply. URLs do not reduce image-token usage or guarantee a lower inference charge.

If text or other attachments keep the body above the budget, the request fails before publication. A publication failure stops inference and triggers the selected backend's cleanup. The proxy does not retry paid inference automatically.

## Cloudflare quick tunnel

Install `cloudflared` to use **Cloudflare quick tunnel**, the default for new profiles. Existing users can select it under **Settings → Large images**. Kilo Proxy starts its own foreground process and a separate loopback image server. The tunnel publishes only registered images at unguessable URLs; it does not expose the administration panel, inference endpoints, or arbitrary local files.

Cloudflare quick tunnels use a temporary `trycloudflare.com` hostname without an account or custom domain. Cloudflare describes them as a development and testing service without an uptime guarantee. See its [Quick Tunnels documentation](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/trycloudflare/).

Images remain available while the request is active, including while a streaming response is being read. Completion, failure, or cancellation removes that request's image URLs. The empty image server and owned tunnel process can be reused by later requests and remain open until Kilo Proxy quits. Switching modes does not stop an already started tunnel. A stopped tunnel is replaced when a new request needs it. Keep the computer and network connection available until inference finishes.

## Litterbox

Select **Litterbox** and choose **1 hour** (default), **12 hours**, **24 hours**, or **72 hours**. The HTTP uploader is part of Kilo Proxy's single binary: it needs no account, storage configuration, or additional executable.

Images leave the computer and are stored by Litterbox. The selected expiry is a request to that service, not a deletion guarantee verified by Kilo Proxy. **The API has no early-delete operation.** Completion, cancellation, switching modes, and quitting Kilo Proxy cannot remove an uploaded copy before its service-managed expiry.

Litterbox's [official FAQ](https://litterbox.catbox.moe/faq.php) says it stores the uploader's IP address with uploaded files and requires prior approval to use Catbox for commercial services. Review that policy before selecting Litterbox for company or commercial use. The available expiry values and upload interface are documented in its [official API tools page](https://litterbox.catbox.moe/tools.php).

## Tailscale Funnel

Install Tailscale and its CLI, sign in, and enable Funnel for the device before selecting **Tailscale Funnel**. The tailnet needs MagicDNS, HTTPS certificates, and a policy that permits Funnel. Follow the [official Funnel setup guide](https://tailscale.com/docs/features/tailscale-funnel); Kilo Proxy does not sign in or enable these account settings for you.

Kilo Proxy uses **HTTPS port 8443** and an independent loopback image server. It checks existing Serve/Funnel configuration and refuses to replace a route that conflicts with its use of that port. It never runs a global Serve/Funnel reset. Its own foreground process provides the route, which can be reused for later requests until Kilo Proxy quits. Switching modes does not stop an already started tunnel. Application cleanup stops only the resources that Kilo Proxy started.

The Funnel URL is public, including to a model provider outside your tailnet. Registered image URLs are unguessable, but anyone who obtains one can retrieve the image while it is active. The server publishes no administration routes, inference routes, or arbitrary local paths. Request completion, failure, or cancellation removes that request's image URLs; application shutdown also closes the server and owned process. Existing Tailscale routes remain under your control.

## Tunnel dependencies

Cloudflare and Tailscale are optional external dependencies. **They are not bundled with Kilo Proxy or installed automatically.** Off, local compression, Kilo uploads, and Litterbox need no extra image-transport executable.

| System | Cloudflare | Tailscale |
| --- | --- | --- |
| macOS | Run `brew install cloudflared` with Homebrew, or use the official Darwin download. | Install Tailscale and make its CLI available to Kilo Proxy; sign in and configure Funnel. |
| Windows | Run `winget install --id Cloudflare.cloudflared --exact`, or download `cloudflared.exe` and make it available on `PATH`. | Install Tailscale with its CLI, then sign in and configure Funnel. |
| Linux | Install the Cloudflare package or binary and make it available on `PATH`. | Install Tailscale and its daemon, then sign in and configure Funnel. |

Use the official [Cloudflare downloads](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/downloads/) and [Tailscale installation instructions](https://tailscale.com/download). The executable must be visible to the desktop app's environment, which can differ from an interactive shell's `PATH`. A missing executable or unavailable service produces an error for the chosen backend; it does not select another mode.

### Startup installation notice

When Cloudflare is selected and `cloudflared` cannot be found, the desktop app and browser helper show a notice on opening and after starting the proxy. It offers installation instructions, a copyable installation command on macOS/Windows, and **Check again**. The check searches the app's PATH and common installation folders; it does not run an installer, start a tunnel, publish images, or contact Cloudflare.

**Not now** dismisses the startup notice for the current UI session. The notice can appear again when the proxy starts or Cloudflare is selected again. **Settings → Large images** always shows the current dependency status while Cloudflare is selected. After installation, use **Check again**; if the executable is still missing, make it accessible to the desktop app and reopen Kilo Proxy when its inherited PATH needs updating.

A missing executable does not block the proxy from starting or handling ordinary requests. Oversized requests that need Cloudflare still require it; the proxy does not silently switch to compression or another service. You can explicitly select **Compress locally** or **Off** in Large images. A detected executable only confirms installation, not tunnel connectivity. The tunnel starts lazily when an oversized image request needs it.

The Windows command uses the [Cloudflare.cloudflared package in Microsoft's WinGet repository](https://github.com/microsoft/winget-pkgs/tree/master/manifests/c/Cloudflare/cloudflared). Package-manager installation may require its normal permissions. Linux instructions link to Cloudflare's packages and downloads so users can choose their distribution and architecture.

## Experimental Kilo uploads

This mode uses Kilo's own Cloud Agent attachment storage with the configured account. **Reusing that storage for Gateway requests is not documented as a supported Gateway integration.** It may change or stop working. No tunnel, storage account, bucket configuration, or extra executable is needed.

The proxy uploads original bytes and sends temporary links to the model provider. Kilo's [attachment constants](https://github.com/Kilo-Org/cloud/blob/main/apps/web/src/lib/cloud-agent/constants.ts) specify a 15-minute link lifetime; the request's 10-minute timeout leaves a margin. When the response body finishes, including streaming, or the request fails or is canceled, the proxy requests deletion. Cleanup uses a separate bounded context so client cancellation does not immediately cancel deletion. Failed deletions are retried within a **30-second cleanup window**, with up to **three attempts**.

If deletion cannot be confirmed, **Settings → Large images** displays **Image cleanup needs attention**. Changing modes prevents Kilo uploads for new requests but does not stop cleanup already in progress or erase the warning.

**Link expiry does not mean file deletion.** Normal quit waits for bounded cleanup. Upload bookkeeping and cleanup warnings are kept only in memory. Network failures, force quitting, a crash, or machine shutdown can leave remote copies behind. Restarting the app does not recover a deletion queue or prove earlier files were removed. Kilo Proxy does not claim a storage-retention guarantee from Kilo.

The storage behavior is based on Kilo's [Cloud Agent pending attachments implementation](https://github.com/Kilo-Org/cloud/blob/main/apps/web/src/lib/r2/cloud-agent-pending-uploads.ts), rather than a public Gateway attachment contract.

## Privacy and capture

Anyone who obtains an active image URL can download the image without a Kilo API key. Tunnel cleanup stops future access through the local server but cannot retract copies already fetched. Litterbox and Kilo store remote copies with the different cleanup behavior described above.

URL modes do not enable request capture or create a new local image-history file. If capture is enabled separately, response bodies for URL-backed requests are omitted because models can repeat temporary links across streaming chunks. Request metadata and the redacted outbound request remain available. Keep captures private; arbitrary prompt secrets are not automatically detected.

## Validation

For v0.31.0, the live Cloudflare test passed: public downloads matched the original bytes and returned HTTP 404 after lease cleanup. Litterbox returned HTTP 412 (`No file!`) or HTTP 403 from the test network, including with the documented standalone curl example. Its multipart and lifecycle tests pass, but a successful live upload has not been confirmed; the option is marked experimental. Tailscale is covered by automated process, capability, conflict, and cleanup tests; a live signed-in Tailscale account was not available for this release check.

Automated tests use synthetic images, storage, tunnel processes, and Gateway services. They exercise settings persistence, supported image positions across all three inference APIs, unchanged URL-backed image bytes, deduplication, limits, explicit failure without fallback, and cleanup without paid inference. Browser settings checks use the real Go backend in isolated temporary profiles. Passing these tests does not establish live availability of every external service.

An optional backend test uses `KILO_IMAGE_BACKEND_LIVE=cloudflare`, `litterbox`, or `tailscale`. It publishes synthetic images, checks their public downloads, and exercises backend cleanup. It requires the selected backend's normal dependencies and network access, but **no Kilo account or inference credits**. Litterbox test uploads remain until expiry because that service has no early-delete API. This test is opt-in and is not enabled in CI.

```sh
KILO_IMAGE_BACKEND_LIVE=cloudflare go test -run '^TestImageURLBackendLive$' -count=1 -v .
```

The separate Kilo Gateway test creates four synthetic images, verifies byte-for-byte downloads, sends one bounded inference, and verifies deletion through the production Go request pipeline. It reads a chosen saved profile without changing it or restarting the running app. **This test uses Kilo account credits** and is not enabled in CI:

```sh
KILO_IMAGE_UPLOAD_LIVE_TEST=1 \
KILO_IMAGE_UPLOAD_CONFIG_DIR="$HOME/Library/Application Support/kilo-proxy" \
go test -run '^TestImageUploadsLiveGateway$' -count=1 -v .
```

A successful live test establishes observed behavior at that time, not a stable service contract. See also [payload limits and recovery](codex-images.md#payload-limits-and-413-errors).
