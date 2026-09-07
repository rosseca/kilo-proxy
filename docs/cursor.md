# Cursor with Kilo Local

Kilo Local can start a dedicated **ngrok HTTPS tunnel** for Cursor. Cursor sends BYOK requests through its servers, so an ordinary localhost URL cannot work.

## One-time setup

1. Install [ngrok 3](https://ngrok.com/download) for macOS, Linux, or Windows. Put the executable on your PATH and restart Kilo Local. Homebrew locations on macOS, `~/.local/bin/ngrok`, and Scoop's Windows shim are also detected.
2. Create an ngrok account and run `ngrok config add-authtoken YOUR_NGROK_AUTHTOKEN` using the token from your ngrok dashboard. This is **not** your Kilo API key. Kilo Local uses ngrok's existing configuration without reading or copying the token.
3. Sign in to Kilo Local, select your organization, and start the local proxy.

## Connect

1. Open the **Cursor** tab. Select a model from the Kilo catalog and click **Add to Cursor list**. Repeat for the models you want (up to 50).
2. Click **Connect Cursor**. This explicitly publishes an authenticated inference endpoint through your ngrok account. Keep the app and computer running while using Cursor.
3. Click **Test public connection**. This checks HTTPS, authentication, and the selected model list without making a billable inference request.
4. In **Cursor → Settings → Models**, enable **OpenAI API Key**, paste the helper's **Cursor key**, and enable **Override OpenAI Base URL** with the helper's HTTPS URL, including `/v1`.
5. Add each exact model ID from the helper using **Add Custom Model / Add model**, and enable it. Select that custom model in a new chat and send a small test request. UI labels vary with Cursor versions.
6. Inspect **Session activity** in Kilo Local for the actual request, upstream response, and any gateway error. Cost and cache statistics use the same pipeline as other clients when Kilo returns usage data.

The OpenAI key field receives the dedicated `kl_cursor_…` key, never your Kilo API key or the ordinary `kl_local_…` key. Do not enter a second organization header in Cursor; the proxy supplies it.

**Disconnect / revoke key** stops the tunnel and its listener. Stopping the main proxy or quitting Kilo Local also disconnects Cursor. Each connection gets a fresh key. Reconnect and update Cursor's key (and URL if changed). Changing the model list requires disconnecting first. Tunnels do not restart automatically. Your ngrok account's endpoint, bandwidth, concurrent-session, and pricing limits apply.

## Scope and limitations

- Only authenticated `GET /v1/models` and `POST /v1/chat/completions` are exposed. The list contains only selected models; other model IDs are rejected. Requests are limited to 16 MiB and eight concurrent generations.
- The tunnel does not expose the control panel, credential store, Responses/Messages routes, or other local services. The ordinary local proxy retains its Host and Origin restrictions.
- Chat Completions streaming, cancellation, tool-call payloads, Kilo authentication, organization routing, and existing usage capture pass through the inference pipeline.
- Prompts and responses travel through **Cursor, ngrok, and Kilo**. Local ngrok traffic inspection is disabled. Cloud metadata and any account-enabled full capture follow your ngrok settings; this feature does not promise zero retention.
- This does not replace **Cursor Tab, Composer, Auto, or every native model**. Cursor documents BYOK for chat models and OpenAI BYOK for standard non-reasoning models. Start with a compatible chat model. Agent tools, reasoning, images, and routing remain dependent on the selected model and Cursor version.
- The base URL override can affect built-in models. Turn off the override/OpenAI key to return to Cursor's native providers. Do not enable multiple API providers and assume each has its own base URL.
- There is no documented provider-settings automation API. The helper supplies copy buttons; it does not write invented settings or modify Cursor's credential database.
- Cloudflare **Quick Tunnels do not support SSE**, so they are not used. A managed/named enterprise tunnel is a separate deployment option.

## Troubleshooting

- **Install ngrok / could not start:** check `ngrok version`, `ngrok config check`, and that your GUI can find the executable. Restart Kilo Local after installing it.
- **ngrok stopped / startup timeout:** check account authentication, allowed endpoints, existing ngrok sessions, quotas, and corporate network rules. Disconnect and reconnect after resolving the issue.
- **Public connection check fails:** check ngrok first. Do not substitute localhost in Cursor; its backend cannot reach it.
- **401:** copy the current Cursor key. A previous connection's key is invalid.
- **400 model not enabled:** add the exact requested Kilo model ID in the helper and reconnect, then select that model in Cursor. No silent fallback or model substitution is performed.
- **404:** verify the base URL ends in `/v1`, not `/v1/chat/completions`; this adapter serves Chat Completions only.
- **No request in Session activity:** Cursor did not reach the inference handler. Check the public connection, selected custom model, key, and URL override. Authentication and rejected ingress requests do not call Kilo.
- **Kilo error in Session activity:** inspect the response for organization credits, model access, or unsupported parameters. Listing a model does not establish inference compatibility.

## Verification

The Go suite checks authentication, route isolation, model restrictions, SSE flushing, credential rewriting, and shutdown. The opt-in `TestCursorLiveTunnel` uses the installed ngrok account with a synthetic local upstream. It creates a temporary real HTTPS tunnel, validates unauthenticated rejection and admin isolation, reads the first streaming event before completion, and stops the tunnel. It does not use Kilo credentials or spend model credits:

```sh
KILO_CURSOR_LIVE_TEST=1 go test -run TestCursorLiveTunnel -v -count=1
```

The live tunnel test passed on macOS with ngrok 3.39.9. A native Cursor conversation has not yet been verified in this development environment; do not interpret the tunnel check as proof that every Cursor model or Agent feature is supported.

## Sources

- [Cursor BYOK and server routing](https://prod.cursor.com/help/models-and-usage/api-keys)
- [Cursor support: localhost and custom endpoint routing](https://forum.cursor.com/t/how-can-i-use-a-local-llm-on-my-desktop-ai-computer/152419/8)
- [Cursor support: base URL override and native models](https://forum.cursor.com/t/does-adding-a-custom-model-override-cursor-s-native-models/157521)
- [ngrok agent commands](https://ngrok.com/docs/gateway/agent/cli)
- [ngrok traffic inspection](https://ngrok.com/docs/obs/traffic-inspection)
- [Cloudflare Quick Tunnel limitations](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/trycloudflare/)
