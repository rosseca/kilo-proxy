# Kilo Proxy

Use your organization’s Kilo credits in your preferred editor. Kilo Proxy is a Go proxy with its own native desktop interface and a menu bar / system tray icon. It adds the organization header that many API clients cannot send themselves.

Connect with your personal Kilo account, choose your organization, and copy a local URL and key into your editor. The native interface uses Gio and operating-system graphics APIs. Windows runs from a standalone executable without a WebView2 installer or an additional UI runtime. Downloaded binaries require no Go, Node, Docker, or Electron. This is an independent companion, not an official Kilo product.

[Download the latest release](https://github.com/rosseca/kilo-proxy/releases/latest) · [Native desktop guide](docs/desktop.md) · [Client setup](docs/clients.md) · [Security and debugging](docs/security-and-debugging.md) · [Development and releases](docs/releases.md)

**Native desktop migration:** this branch is under pull-request review. Existing published releases may still use the browser interface. This change does not publish a release or increment `VERSION`.

**Name change:** Kilo Proxy was previously called Kilo Local. New builds use `Kilo Proxy.app`, `Kilo Proxy.exe`, or `kilo-proxy`, and archive names begin with `kilo-proxy-`. Existing credentials, application configuration and editor profiles stay in their current locations. Keep provider IDs such as `kilo-local` and environment names such as `KILO_LOCAL_API_KEY` unchanged; the display-name change does not require signing in again or rebuilding profiles.

## Get started

1. Download the archive for your operating system and architecture from **Releases**, then extract it.
2. Open **Kilo Proxy.app** on macOS, **Kilo Proxy.exe** on Windows, or run `./kilo-proxy` on Linux.
3. Click **Sign in with Kilo / SSO** and approve the device code on Kilo’s website using your usual login or SSO. Choose your organization, then click **Save & start**. Manual API key and organization ID entry is also available.
4. Open **Clients & models** and choose your editor. Select models, click **1. Prepare profile** where available, then copy its launch or connection instructions.

The default API URL is `http://127.0.0.1:8877/v1`. The editor uses a randomly generated **local API key**, not your personal Kilo key. Enable **Remember** to save the upstream credential in the operating system’s credential store when saving the connection.

**Check gateway** retrieves the model catalog without paid inference. Catalog access does not prove organization balance or permission to generate with a model. Verify those with a request from your editor and its attribution in Kilo.

Closing the application window leaves the proxy running. The system tray / menu bar icon can reopen the interface, show status and counters, start or stop the saved connection, and quit the application. Stopping cancels active requests. The proxy does not start automatically when opening the application.

## Downloads

| System | Architecture | Archive contents |
| --- | --- | --- |
| macOS | Apple Silicon / Intel | `.app` bundle in ZIP; macOS 12 or later |
| Windows | x64 / ARM64 | Standalone native GUI executable in ZIP |
| Linux | x64 / ARM64 | Native executable in TAR.GZ; system graphics libraries; optional `install-user.sh` launcher |

Every release includes six archives and `SHA256SUMS.txt`. Linux’s installer adds an application-menu entry for the current user without administrator privileges. Linux tray support requires a graphical session with D-Bus and StatusNotifierItem/AppIndicator support; GNOME may need an AppIndicator extension. Use `--browser` for the optional browser interface or `--no-tray` for headless mode. See [desktop dependencies and launch options](docs/desktop.md) for Linux graphics requirements. Desktop builds target macOS, Windows and Linux; there are no mobile packages.

macOS bundles have an **ad-hoc signature** covering the executable, bundle metadata, and resources. They are **not Developer ID signed or notarized**; Windows binaries are unsigned. macOS and Windows may show origin warnings. Company-wide managed distribution can add publisher signing and macOS notarization separately. The project does not install an auto-updater or change system startup settings.

If macOS reports that **Kilo Local** does not respond when opening a `0.20.0` or older download, replace it with `0.20.1` or later. Those historical releases used the former application name. Older ZIPs contained a linker-signed executable without a complete app-bundle signature. The release pipeline now signs and verifies the complete bundle on macOS, verifies it again after extraction, and tests native app launch and graceful quit. Move the replacement app to Applications before opening it. If macOS shows an unidentified-developer warning, follow Apple's [Open Anyway instructions](https://support.apple.com/guide/mac-help/mh40616/mac); this is separate from a broken bundle signature.

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

**Cursor connects through a dedicated HTTPS tunnel.** Install and configure ngrok once, select models in the Cursor helper, and click **Connect HTTPS tunnel**. Copy its URL and dedicated key into Cursor. [Setup, privacy, and compatibility limits](docs/cursor.md).

The model helpers support catalog search, manual IDs, context metadata, and input/output prices in USD per million tokens. Prices come from Kilo’s catalog, not your invoice. Explicitly free prices show zero; variable or missing prices remain unavailable. Refreshing models does not run inference. Model selections are independent between client tabs and remain available during the application session.

Sort every model picker by **Code Mode Rank**, **Coding Index**, **Speed**, **Price**, or **Name**. The default is Kilo's seven-day Code mode usage rank; price ordering uses input cost. Missing metrics appear last, and sorting preserves your selections and initial model. [Sources and sorting behavior](docs/clients.md#sort-the-model-catalog).

For Codex Desktop, select models, choose an initial model and reasoning level, and edit display names to shorten labels. Click **1. Prepare profile** to create the isolated profile folder and save or update both `config.toml` and `models.json`, preserving unrelated settings and backing up changed files. Then copy the launch command; close the Kilo instance first if it is already running. The actual Kilo model IDs remain unchanged.

Claude Code has the same select-and-prepare flow in its own tab. It detects the installed version, writes a separate profile with backups, and enables supported native model names and reasoning preferences. See [client setup](docs/clients.md) for profile isolation, saving, and compatibility limits.

OpenCode and Zed now have the same select-and-prepare workflow, with saved selections and JSONC-preserving updates. OpenCode includes local authentication in its dedicated profile; Zed uses a one-time key paste into its keychain-backed provider settings. See [their setup guide](docs/opencode-and-zed.md).

The application and tray support **English / Español**. The first launch reads the operating system’s preferred language, with English as the fallback. An explicitly saved language takes priority; changing it updates the interface and tray without restarting the proxy. Documentation and release instructions are in English.

## Inspect recent requests

In **Activity & costs → Recent requests**, click a completed request to open its inspector. View the original client request, the request forwarded to Kilo, Kilo’s response, and the response returned to the client. Each stage includes headers and body, including tool calls and SSE data.

The last 30 requests are kept in memory only. Capture starts enabled and can be paused or cleared. Authentication headers and known keys are redacted in debug copies; arbitrary secrets inside prompts are not automatically detected. See [capture limits and security details](docs/security-and-debugging.md).

## Track observed spend

**Activity & costs** shows session cost in reported USD, input/output/cache tokens, and a conversation breakdown. It listens to responses passing through the proxy, including streaming, and keeps totals independently of the last 30 debug captures. Codex task IDs and Claude Code session IDs are used when present; requests without an identifier are marked unassigned.

**Cache reuse** shows tokens read from cache, tokens written to cache, and the share of input reused. Conversation details include cumulative figures and the last request’s cache read / total input. Ratios use complete, comparable usage records; missing cache data is never treated as zero.

Costs that are missing stay **Not reported**, with coverage shown alongside the total. A canceled response may not deliver final billing data. Totals last until the app closes and are gateway observations, not a Kilo invoice or catalog estimate. See [accounting fields and limits](docs/security-and-debugging.md#passive-spend-tracking-0130).

## Develop and release

Requirements: Go 1.26 or later, Python 3.9 or later for packaging, and Node 22 or later for helper tests. Runtime users do not need these tools.

```sh
go mod download
go run -tags desktop .
```

Desktop builds require platform development tools on macOS/Linux; Windows uses its built-in graphics APIs. See [native build instructions](docs/desktop.md#building-locally). For the optional browser interface, run `go run . --browser`.

For an isolated headless development profile:

```sh
go run . --no-browser --no-tray --config-dir ./tmp-profile
```

The printed panel URL contains an administrative token; keep it private.

```sh
go test -race ./...
go vet ./...
node --test scripts/*.test.mjs
python3 -m unittest discover -s scripts -p 'test_*.py'
# On a host with native graphics dependencies:
go test -tags desktop ./...
python3 scripts/package.py --build-only
```

Build outputs go to `dist/` and are excluded from Git. `VERSION` is the single default version source for the application and packages. A future matching version tag runs the reusable validation workflow and publishes six verified archives with checksums and generated notes. Packaging waits for the core, browser and native jobs, packages their tested executables, and checks the extracted archives before publication. The native-interface migration is delivered separately as a pull request; no tag is created for it. It uses the repository’s built-in `GITHUB_TOKEN`; no personal release token is required.

See [the release guide](docs/releases.md) for the exact commands, prereleases, recovery, and verification.

## Compatibility and validation

Automated tests cover authentication replacement, host/origin restrictions, lifecycle and cancellation, login states, catalog normalization, schema adaptation, streaming, trace redaction, and client configuration helpers. They use simulated credentials and gateways. Native control tests and executable smoke checks exercise the desktop interface separately from browser-mode Playwright tests. Cross-compilation alone does not prove native credential-store or tray behavior, and tests do not perform paid model inference. Consult the checks for a specific commit or pull request for actual results.

Codex catalog loading and reasoning/display-name metadata were checked against the installed app-server. Desktop isolation depends partly on version-specific application behavior: see [inspection notes](docs/codex-desktop-compatibility.md). Kilo must support the protocol and model you choose, and your organization must permit it.

Dependency versions are pinned in `go.mod` and `go.sum`. Licensing notices are in [THIRD-PARTY-NOTICES.txt](THIRD-PARTY-NOTICES.txt); local Gio platform changes are documented alongside `third_party/gio`, and the Windows tray ABI correction alongside `third_party/fyne-systray`. The old `third_party/systray` source remains archived reference material.

See [Xcode setup](docs/xcode.md) for Chat provider registration and the dedicated Codex/Claude agent profiles.
