# Kilo Local

Use your organization’s Kilo credits in your preferred editor. Kilo Local is a Go proxy with a browser control panel and a native menu bar / system tray icon. It adds the organization header that many API clients cannot send themselves.

Connect with your personal Kilo account, choose your organization, and copy a local URL and key into your editor. Downloaded binaries require no Go, Node, Docker, or Electron. This is an independent companion, not an official Kilo product.

[Download the latest release](https://github.com/rosseca/kilo-proxy/releases/latest) · [Client setup](docs/clients.md) · [Security and debugging](docs/security-and-debugging.md) · [Development and releases](docs/releases.md)

## Get started

1. Download the archive for your operating system and architecture from **Releases**, then extract it.
2. Open **Kilo Local.app** on macOS, **Kilo Local.exe** on Windows, or run `./kilo-local` on Linux.
3. Click **Connect with Kilo** and approve the device code on Kilo’s website using your usual login or SSO. Choose your organization, then click **Save & start**. Manual API key and organization ID entry is also available.
4. Open your editor’s tab in the control panel. Select models and copy its generated configuration and launch instructions.

The default API URL is `http://127.0.0.1:8877/v1`. The editor uses a randomly generated **local API key**, not your personal Kilo key. Enable **Remember** to save the upstream credential in the operating system’s credential store when saving the connection.

**Check gateway** retrieves the model catalog without paid inference. Catalog access does not prove organization balance or permission to generate with a model. Verify those with a request from your editor and its attribution in Kilo.

Closing the browser leaves the proxy running. The **K** menu can reopen the panel, show status and counters, start or stop the saved connection, and quit the application. Stopping cancels active requests. The proxy does not start automatically when opening the application.

## Downloads

| System | Architecture | Archive contents |
| --- | --- | --- |
| macOS | Apple Silicon / Intel | `.app` bundle in ZIP; macOS 12 or later |
| Windows | x64 / ARM64 | GUI executable in ZIP |
| Linux | x64 / ARM64 | Portable binary in TAR.GZ; optional `install-user.sh` launcher |

Every release includes six archives and `SHA256SUMS.txt`. Linux’s installer adds an application-menu entry for the current user without administrator privileges. Linux tray support requires a graphical session with D-Bus and StatusNotifierItem/AppIndicator support; GNOME may need an AppIndicator extension. Without a compatible tray, use the browser panel or `--no-tray`.

macOS bundles have an **ad-hoc signature** covering the executable, bundle metadata, and resources. They are **not Developer ID signed or notarized**; Windows binaries are unsigned. macOS and Windows may show origin warnings. Company-wide managed distribution can add publisher signing and macOS notarization separately. The project does not install an auto-updater or change system startup settings.

If macOS reports that Kilo Local does not respond when opening a `0.20.0` or older download, replace it with `0.20.1` or later. Older ZIPs contained a linker-signed executable without a complete app-bundle signature. The release pipeline now signs and verifies the complete bundle on macOS, verifies it again after extraction, and tests native app launch and graceful quit. Move the replacement app to Applications before opening it. If macOS shows an unidentified-developer warning, follow Apple's [Open Anyway instructions](https://support.apple.com/guide/mac-help/mh40616/mac); this is separate from a broken bundle signature.

## Editors and models

| Client | What the helper configures |
| --- | --- |
| Codex Desktop | Separate GUI profile, multiple models, short display names, native reasoning selector |
| Codex CLI | Same automatic helper: independent multi-model profile, short names, reasoning levels and terminal launcher |
| OpenCode | Automatic profile preparation, multiple models, names, limits, and scoped launcher |
| Claude Code | Automatic isolated profile, version-aware model picker, short names, native effort and terminal launcher |
| Zed | Automatic JSONC settings updates, multiple models, names, and initial model |
| Xcode | Independent Chat model list, automatic Codex/Claude agent profiles, version-aware Claude aliases and setup guidance |
| Cursor | Managed ngrok HTTPS connection, dedicated key, selected models, and public connection check |

**Cursor connects through a dedicated HTTPS tunnel.** Install and configure ngrok once, select models in the Cursor helper, and click **Connect Cursor**. Copy its URL and dedicated key into Cursor. [Setup, privacy, and compatibility limits](docs/cursor.md).

The catalog supports search, tool-capable text-model filtering, manual IDs, context metadata, and input/output prices in USD per million tokens. Prices come from Kilo’s catalog, not your invoice; zero, variable, and missing prices are distinguished. Refreshing models does not run inference. Model selections are independent between client tabs and remain available during the panel session.

For Codex Desktop, check models directly in one list, choose the initial model and reasoning in each row, and edit **Name in Codex** to shorten labels. Click **Prepare Codex GUI** to create the isolated profile folder and save or update both `config.toml` and `models.json`, preserving unrelated settings and backing up changed files. Then copy the launch command; close the Kilo instance first if it is already running. The actual Kilo model IDs remain unchanged.

Claude Code has the same select-and-prepare flow in its own tab. It detects the installed version, writes a separate profile with backups, and enables supported native model names and reasoning preferences. The GUI includes a comparison explaining the remaining differences from Codex Desktop. See [client setup](docs/clients.md) for profile isolation, saving, and compatibility limits.

OpenCode and Zed now have the same select-and-prepare workflow, with saved selections and JSONC-preserving updates. OpenCode includes local authentication in its dedicated profile; Zed uses a one-time key paste into its keychain-backed provider settings. See [their setup guide](docs/opencode-and-zed.md).

The control panel supports **English / Español**, remembers the selected language, and preserves your edits when switching. Documentation and release instructions are in English. The native tray menu currently uses Spanish labels.

## Inspect recent requests

In **Activity**, click **Inspect** on a completed request to view the original client request, the request forwarded to Kilo, Kilo’s response, and the response returned to the client. Each stage includes headers and body, including tool calls and SSE data.

The last 30 requests are kept in memory only. Capture starts enabled and can be paused or cleared. Authentication headers and known keys are redacted in debug copies; arbitrary secrets inside prompts are not automatically detected. See [capture limits and security details](docs/security-and-debugging.md).

## Track observed spend

**Activity → Observed spend** shows reported USD, input/output/cache tokens, and a session/task breakdown. It listens to responses passing through the proxy, including streaming, and keeps totals independently of the last 30 debug captures. Codex task IDs and Claude Code session IDs are used when present; requests without an identifier are marked unassigned.

**Context cache** shows tokens read from cache, tokens written to cache, and the share of input reused. The conversation table includes cumulative figures and the last request’s cache read / total input. Ratios use complete, comparable usage records; missing cache data is never treated as zero.

Costs that are missing stay **Not reported**, with coverage shown alongside the total. A canceled response may not deliver final billing data. Totals last until the app closes and are gateway observations, not a Kilo invoice or catalog estimate. See [accounting fields and limits](docs/security-and-debugging.md#passive-spend-tracking-0130).

## Develop and release

Requirements: Go 1.26 or later, Python 3.9 or later for packaging, and Node 22 or later for helper tests. Runtime users do not need these tools.

```sh
go mod download
go run .
```

For an isolated development profile:

```sh
go run . --no-browser --no-tray --config-dir ./tmp-profile
```

The printed panel URL contains an administrative token; keep it private.

```sh
go test -race ./...
go vet ./...
node --test scripts/*.test.mjs
python3 -m unittest discover -s scripts -p 'test_*.py'
python3 scripts/package.py
```

Build outputs go to `dist/` and are excluded from Git. `VERSION` is the single default version source for the application and packages. Pushing a matching tag such as `v0.12.0` runs tests on macOS, Linux, and Windows, builds all six archives, and publishes a GitHub release with checksums and generated notes. It uses the repository’s built-in `GITHUB_TOKEN`; no personal release token is required.

See [the release guide](docs/releases.md) for the exact commands, prereleases, recovery, and verification.

## Compatibility and validation

Automated tests cover authentication replacement, host/origin restrictions, lifecycle and cancellation, login states, catalog normalization, schema adaptation, streaming, trace redaction, and client configuration helpers. They use simulated credentials and gateways. Cross-compilation does not prove native credential-store or tray behavior on every operating system, and tests do not perform paid model inference.

Codex catalog loading and reasoning/display-name metadata were checked against the installed app-server. Desktop isolation depends partly on version-specific application behavior: see [inspection notes](docs/codex-desktop-compatibility.md). Kilo must support the protocol and model you choose, and your organization must permit it.

Dependency versions are pinned in `go.mod` and `go.sum`. Licensing notices are in [THIRD-PARTY-NOTICES.txt](THIRD-PARTY-NOTICES.txt); local tray-library changes are documented in [PATCHES.md](third_party/systray/PATCHES.md).

See [Xcode setup](docs/xcode.md) for Chat provider registration and the dedicated Codex/Claude agent profiles.
