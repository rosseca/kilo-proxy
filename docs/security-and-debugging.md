# Credentials, network behavior, and debugging

## Login and credential storage

**Connect with Kilo** uses the device authorization flow implemented by Kilo’s public CLI:

1. `POST https://api.kilo.ai/api/device-auth/codes` creates a temporary code.
2. You approve the code on Kilo’s HTTPS website with your usual login or company SSO.
3. The Go backend polls every three seconds and receives an individual token after approval.
4. `GET https://api.kilo.ai/api/profile` supplies organization membership. One organization is selected automatically; for multiple organizations, choose one yourself.

Kilo Local does not receive your password, browser cookies, MFA, or SSO-provider credentials. It does not create or obtain a shared team master key. Every user retains their own identity; the proxy adds the organization ID. SSO availability and access restrictions depend on your company’s Kilo configuration. If no teams are returned, the app reports this instead of silently switching to personal billing. Changing accounts clears the previous organization selection.

The device/profile endpoints come from Kilo’s implementation rather than a stable third-party API contract. Manual API key and organization ID entry remains available. Canceling login prevents that attempt from applying locally; it does not revoke an already issued remote token. **Forget key** removes the local copy only. For an expired or revoked token, sign in again.

The Kilo credential stays in Go memory unless **Remember** is enabled when saving or checking the connection. Persistent storage uses macOS Keychain, Windows Credential Manager, or Linux Secret Service over D-Bus. There is no silent plaintext fallback if the credential store fails; session-only operation remains available.

`settings.json` stores the port, organization ID, credential-store identifier, language, and **local API key**, not the upstream Kilo key. Unix uses a `0700` configuration directory and `0600` settings file; Windows inherits profile permissions. By default the directory is `kilo-proxy` beneath Go’s `os.UserConfigDir()`. `--config-dir` selects a different directory. The local key remains stable after saved configuration is reloaded and allows spending credits while the proxy is running.

The real device-flow start and pending polling were checked without approval. Automated tests simulate approval, cancellation, denial, expiry, organization selection, and profile errors. Company SSO approval and native credential-store behavior require validation with your own account and operating system.

## Local access and upstream requests

- The inference listener binds IPv4 `127.0.0.1` only. Use the exact URL from the panel.
- The panel uses a random loopback port and a separate administrative token. Its startup link carries that token in the fragment; the UI removes it from the URL and keeps it in `sessionStorage`. Do not share the startup link.
- Host and Origin checks restrict access from other web pages. The inference port rejects browser-origin requests and is intended for local API clients. Containers, WSL, and remote computers may have different loopback networks.
- Production inference goes only to `https://api.kilo.ai/api/gateway`; arbitrary upstream URLs and environment HTTP proxies are not used.
- Client authentication and organization headers are replaced. Cookies, alternative credentials, and client-supplied organization overrides are not forwarded.
- Loopback restrictions do not isolate malicious processes running as the same user.

| Local endpoint | Kilo endpoint |
| --- | --- |
| `GET /v1/models` | `GET /api/gateway/models` |
| `POST /v1/chat/completions` | `POST /api/gateway/chat/completions` |
| `POST /v1/responses` | `POST /api/gateway/responses` |
| `POST /v1/messages` | `POST /api/gateway/messages` |

Clients send `Authorization: Bearer <local-key>`. The proxy substitutes the Kilo credential and adds `X-KiloCode-OrganizationId`. Only the exact optional query `beta=true` is accepted on Messages; other queries and endpoints are rejected.

Protocols are preserved, subject to the [Anthropic tool-schema bridge](clients.md#fable--anthropic-tool-schemas). Availability depends on Kilo and the selected model; the proxy does not translate between Chat Completions, Responses, and Messages. JSON, tool calls, upstream errors, and SSE otherwise pass through. Cancellation propagates upstream. Uploads are limited to 32 MiB; reading a request and waiting for initial upstream headers each have a two-minute limit. There is no write timeout that cuts off a long streaming response.

The catalog is public and can be retrieved without login. When credentials and an organization are saved, both are included in the backend request. Only normalized metadata reaches the browser. Changing the connection invalidates prior catalog requests. Catalog access and prices do not prove balance, negotiated discounts, or organization policy.

## Activity inspector

**Inspect** opens four stages for a completed request:

1. Client → Local: original request headers and body.
2. Local → Kilo: request after authentication and tool-schema adaptation.
3. Kilo → Local: gateway response.
4. Local → Client: response after any adaptation.

This works for Codex and other clients. Local failures after client validation also appear, including schema-bridge errors. Duration covers the complete request rather than time to first token. Local cancellations are recorded as `499`.

Capture starts enabled each application launch. **Capture details** pauses it for new requests. **Clear history** removes existing rows and details and prevents older in-flight requests from restoring them; session counters remain. Closing the application discards the history.

Limits are 30 completed entries, 128 KiB per body, and 32 KiB per header set, with truncation indicators. At most 16 concurrent requests capture bodies; additional requests retain activity rows without details. Traces are held in memory only and never written to disk.

Authentication, cookie, and credential headers are redacted. Known upstream, local, and administrative keys are also replaced in captured bodies. Redaction affects debug copies, not actual traffic; it does not detect every secret a prompt might contain. Inspect content before sharing it.

The authenticated details endpoint is separate from lightweight status polling. Streaming continues while data is copied; details appear after completion or cancellation. JSON formatting adds whitespace without changing large numbers, duplicate keys, or literal values. SSE and incomplete bodies remain verbatim. Headers represent HTTP objects visible to the proxy, not a TCP capture; the transport can add protocol headers.

## System tray and troubleshooting

The K menu shows running/stopped/configuration/login status, organization, port, active requests, and session counters. It can reopen the panel, start the saved configuration, stop and cancel requests, or quit. Unsaved form changes do not apply through the tray. Port conflicts open the panel for correction. Running status confirms the local server, not Kilo availability or credits.

The tray updates approximately once per second when the native menu processes events. macOS uses a template icon for light/dark menu bars; Windows may place the icon in the hidden-icons area. Linux requires a compatible StatusNotifierItem/AppIndicator host and D-Bus. No extension is installed automatically.

Use `--no-tray` for headless operation and `--no-browser` to suppress automatic browser opening. With both enabled, open the printed panel URL manually. Quit from the panel or interrupt the process to stop all listeners. Native tray interaction and credential-store integration still need platform-specific manual verification.

## Implementation references

- [Kilo device authorization](https://github.com/Kilo-Org/kilo/blob/main/packages/kilo-gateway/src/auth/device-auth-tui.ts).
- [Kilo profile and organizations](https://github.com/Kilo-Org/kilo/blob/main/packages/kilo-gateway/src/api/profile.ts).
- [Kilo authentication](https://kilo.ai/docs/gateway/authentication).
- [Cross-platform credential store](https://github.com/zalando/go-keyring).
- [Native tray library](https://github.com/gogpu/systray); local changes are recorded in `third_party/systray/PATCHES.md` in the source repository.
