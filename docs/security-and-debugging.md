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
| `GET /xcode/v1/models` | Local saved Xcode Chat selection; authenticated |
| `POST /xcode/v1/chat/completions` | `POST /api/gateway/chat/completions` |
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

## Passive spend tracking (0.13.0)

Activity shows reported USD costs and token usage for inference requests observed since this application process started. The totals and session breakdown survive clearing the 30-entry trace history and pausing detail capture. Restarting the application resets them; accounting metadata is not saved to disk. Requests already completed before starting this version cannot be recovered.

The reader observes upstream response bytes without changing the request, forwarding session headers, delaying streaming, or making additional inference/billing calls. Chat Completions, Responses, and Messages are handled as JSON or SSE. Cumulative usage snapshots replace prior values within the same request instead of being summed repeatedly. Messages input/cache usage from `message_start` is combined with final output usage. Individual activity rows include the response model and reported cost.

`usage.cost_microdollars`, when present, is converted to USD. Otherwise, numeric `usage.cost` is used as the gateway-reported USD amount. Money is rounded to the nearest nanodollar and summed as integers. Explicit zero is retained. Missing, negative, malformed, or unsupported cost fields remain **Not reported**; no catalog estimate is silently substituted. Provider/BYOK `cost_details.upstream_inference_cost` is not added to a Kilo charge. These observations are not a reconciliation of the organization invoice; cost semantics and availability depend on the Kilo route/provider.

The UI displays how many requests supplied costs and how many responses were incomplete or exceeded parsing limits. Canceled requests can incur charges without delivering final usage; unknown requests do not count as free. Token counters retain provider-reported input/output/cache/reasoning values; cache can be included in input counts for one protocol and separate in another, so these categories must not be added indiscriminately. Models/catalog GET requests do not enter inference-spend totals.

Grouping prefers Codex `thread-id`, then `session-id` (or legacy `session_id`), then `X-KiloCode-TaskId`, then an explicit `X-Kilo-Local-Session`. Codex sends session/thread headers in its [official API client](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/requests/headers.rs); availability depends on the client version. A thread ID groups the conversation; a session ID may identify a wider client session. Without an identifier, requests are explicitly grouped as unassigned rather than guessing from prompts, model, or connection. Organization IDs are part of the grouping key. Up to 200 groups are retained, with additional groups combined under Other sessions.

The usage parser buffers at most 1 MiB per JSON body or SSE data event, independently of the 128 KiB debug capture. Oversized frames are skipped and flagged; a later final SSE usage event can still be read. An interrupted or unterminated SSE frame is not treated as a completed response. The stream itself continues unchanged.

Sources: [Kilo usage and billing](https://kilo.ai/docs/gateway/usage-and-billing), [Kilo streaming](https://kilo.ai/docs/gateway/streaming), and [Kilo client cost extraction](https://github.com/Kilo-Org/kilo/blob/main/packages/opencode/src/session/index.ts). Documentation confirms usage reporting; no paid inference was made to validate a specific model's returned billing fields. Automated tests use representative gateway fixtures.


## Context cache statistics (0.16.0)

The activity panel displays reported cache reads and writes as token counts, plus the percentage of input served from cache. The session/task table shows the same cumulative figures, coverage, and the latest completed proxy request's cache read / total input. An interrupted request can report token counts while its ratio remains unavailable.

The denominator is normalized per protocol:

- OpenAI Responses and Chat Completions: input/prompt tokens already include cached input.
- Anthropic Messages: total input is ordinary input plus cache-read input plus cache-creation input. All three counters must be present; an omitted counter is not assumed to be zero.

The cumulative percentage divides the sum of cache reads by the sum of normalized input for the same complete, non-truncated requests. Coverage states how many requests qualified. Requests without cache data do not silently reduce the ratio, explicit zero cache hits remain zero, and zero input has no meaningful percentage. Reported cache-write tokens are shown separately; writing content is not a cache hit.

The latest request is replaced even when it has no cache information, so a previous cache hit is not presented as current. Counters remain in memory until the app closes and survive clearing debug captures. They count processed tokens, including repeated context across requests, not unique conversation tokens, currently stored cache size, cache expiry or dollar savings.

Primary references: [OpenAI prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching) and [Anthropic prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching).

Xcode agent preparation writes only Apple's dedicated `CodingAssistant/codex` and `CodingAssistant/ClaudeAgentConfig` folders, preserves unrelated settings and backs up changed files. Codex in Xcode uses a file-protected local bearer token because it is not launched by the terminal helper. The Kilo account key is never written to these profiles. Xcode Chat selection filters discovery only; it does not restrict authorized inference model IDs. See [Xcode setup](xcode.md).
