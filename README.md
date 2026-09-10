# Kilo Proxy

Use your organization’s Kilo credits in your preferred editor. Kilo Proxy is a Go proxy with its own native desktop interface and a menu bar / system tray icon. It adds the organization header that many API clients cannot send themselves.

Connect with your personal Kilo account, choose your organization, add models once, and open an installed agent from its card. The native interface uses Gio and operating-system graphics APIs. Windows runs from a standalone executable without a WebView2 installer or an additional UI runtime. Downloaded binaries require no Go, Node, Docker, or Electron. This is an independent companion, not an official Kilo product.

[Download the latest release](https://github.com/rosseca/kilo-proxy/releases/latest) · [Native desktop guide](docs/desktop.md) · [Shared models](docs/shared-models.md) · [Client setup](docs/clients.md) · [Security and debugging](docs/security-and-debugging.md) · [Development and releases](docs/releases.md)

**Native desktop app:** releases from v0.21.0 use the native window and system tray. The browser interface remains available as an optional helper.

## Get started

1. Download the archive for your operating system and architecture from **Releases**, then extract it.
2. Open **Kilo Proxy.app** on macOS, **Kilo Proxy.exe** on Windows, or run `./kilo-proxy` on Linux.
3. The first-run guide opens automatically. Click **Sign in with Kilo / SSO** and approve the device code on Kilo’s website using your usual login or SSO. Choose your team and click **Save & choose models**. **Use an API key or enter a team ID** provides manual entry.
4. Choose at least one model, then **Continue**. Names, order, default and supported reasoning preferences save automatically to the shared library.
5. Click **Start proxy & go to agents**, choose a project folder, and click **Open Codex** or another installed agent. Profiles are prepared automatically from the shared library. Zed also receives its local credential automatically. Cursor and Xcode retain their one-time provider setup under **Options**.

Configured installations open **Agents** directly. **Start proxy** sits beside the stopped status; opening an agent also starts the proxy before launching it. If startup fails, the agent stays closed and the error appears in Kilo Proxy. Incomplete setup can be resumed with **Continue setup**.

The default API URL is `http://127.0.0.1:8877/v1`. The editor uses a randomly generated **local API key**, not your personal Kilo key. Enable **Remember** to save the upstream credential in the operating system’s credential store when saving the connection.

**Check gateway** retrieves the model catalog without paid inference. Catalog access does not prove organization balance or permission to generate with a model. Verify those with a request from your editor and its attribution in Kilo.

Closing the application window leaves the proxy running. The system tray / menu bar can reopen the interface, show status and observed spend, start or stop the saved connection, and quit the application. **Settings → Appearance** saves your choice of **K icon** or **Session cost**; macOS shows the K icon and amount together in **Session cost**, while other tray hosts may use the K icon, tooltip and menu. Stopping cancels active requests. The proxy does not start automatically when opening the application.

On macOS and Linux, **Settings → Terminal commands → Install terminal commands** adds `kilo-codex` and `kilo-claude`. Open a new terminal in your project and run either command to use the latest saved shared models in that terminal. Arguments pass through, including `kilo-codex resume` and `kilo-claude --resume`. Keep Kilo Proxy open, including in the tray; the commands start its saved connection when needed. Your ordinary `codex` and `claude` profiles keep their usual authentication. See [terminal command setup](docs/terminal-commands.md).

## Downloads

| System | Architecture | Archive contents |
| --- | --- | --- |
| macOS | Apple Silicon / Intel | `.app` bundle in ZIP; macOS 12 or later |
| Windows | x64 / ARM64 | Standalone native GUI executable in ZIP |
| Linux | x64 / ARM64 | Native executable in TAR.GZ; system graphics libraries; optional `install-user.sh` launcher |

Every release includes six archives and `SHA256SUMS.txt`. Linux’s installer adds an application-menu entry for the current user without administrator privileges. Linux tray support requires a graphical session with D-Bus and StatusNotifierItem/AppIndicator support; GNOME may need an AppIndicator extension. Use `--browser` for the optional browser interface or `--no-tray` for headless mode. See [desktop dependencies and launch options](docs/desktop.md) for Linux graphics requirements. Desktop builds target macOS, Windows and Linux; there are no mobile packages.

macOS bundles have an **ad-hoc signature** covering the executable, bundle metadata, and resources. They are **not Developer ID signed or notarized**; Windows binaries are unsigned. macOS and Windows may show origin warnings. Company-wide managed distribution can add publisher signing and macOS notarization separately. The project does not install an auto-updater or change system startup settings.

## Editors and models

| Client | What the helper configures |
| --- | --- |
| Codex Desktop | Separate GUI profile, multiple models, short display names, native reasoning selector |
| Codex CLI | Separate generated profile using the shared models, names, reasoning levels and terminal launcher |
| OpenCode | Automatic profile preparation, multiple models, names, limits, and scoped launcher |
| Claude Code | Automatic isolated profile, version-aware model picker, short names, native effort and terminal launcher |
| Zed | Automatic local credentials and JSONC settings updates, multiple models, names, and initial model |
| Xcode | Dedicated Chat model list, automatic Codex/Claude agent profiles, version-aware Claude aliases and setup guidance |
| Cursor | Managed ngrok HTTPS connection, dedicated key, selected models, and public connection check |

**Cursor connects through a dedicated HTTPS tunnel.** Install and configure ngrok once, choose shared models, and use **Agents → Cursor → Set up tunnel → Connect HTTPS tunnel**. Copy its URL and dedicated key into Cursor. [Setup, privacy, and compatibility limits](docs/cursor.md).

The model helpers support catalog search, manual IDs, context metadata, and input/output prices in USD per million tokens. Prices come from Kilo’s catalog, not your invoice. Explicitly free prices show zero; variable or missing prices remain unavailable. Refreshing models does not run inference. The native **Models** library is shared by all agents and saved across restarts in the application configuration directory, separately from generated profiles. It contains model preferences and no API keys. See [shared-model storage and compatibility](docs/shared-models.md). The optional browser helper retains its independent per-client selections.

Browse models in a responsive **card grid**, with names, IDs and input/output prices together. In **Models → Add models**, filter by **lab**, using publishers from your catalog and saved manual models, then sort by **Code Mode Rank**, **Coding Index**, **Speed**, **Price**, or **Name**. The default is Kilo's seven-day Code mode usage rank; price ordering uses input cost. Missing metrics appear last, and filtering and sorting preserve your selections and initial model. [Sources and sorting behavior](docs/clients.md#sort-the-model-catalog).

For Codex Desktop, edit the shared library and click **Open Codex** on **Agents** to prepare the isolated profile folder, save or update both `config.toml` and `models.json`, and open Codex with separate interface data. Unrelated settings are preserved and changed files are backed up. Close the Kilo instance before relaunching if its environment settings changed. Manual preparation and command exports remain available. The actual Kilo model IDs remain unchanged.

**Images in Codex, available from v0.23.0.** **Models → Image generation for Codex** can optionally configure a `generate_image` MCP tool inside Kilo Proxy. Choose an image-output model independently of your coding models. Opening or preparing a Codex profile saves the image setup, separately from the automatic model library. Requests go through the configured Kilo account and organization, images are saved locally, and Activity shows returned usage and its cost source. Provider inference costs, including BYOK, can differ from the organization's Kilo charge. No additional runtime is needed. See [image setup and editing limits](docs/codex-images.md).

From v0.23.1, image results include a preview bounded to **1024 pixels per side and 256 KiB per image**, while the full-resolution original stays saved locally for export and editing. Existing conversations can still contain large inline images; an upstream **413** may require client-side compaction, a new conversation, or fewer attachments. See [payload limits and recovery](docs/codex-images.md#payload-limits-and-413-errors).

The Claude Code card detects the installed version, prepares a separate profile with backups, and opens an interactive terminal. Shared names and reasoning preferences apply only where that version and model support them. See [client setup](docs/clients.md) for profile isolation, saving, and compatibility limits.

OpenCode and Zed receive the shared IDs, names, default and token limits through JSONC-preserving updates. Their reasoning remains automatic. OpenCode includes local authentication in its dedicated profile; Zed receives its local key in the system credential store and refreshes the provider when the key changes, including in an already-open editor. See [their setup guide](docs/opencode-and-zed.md).

The application and tray support **English / Español**. The first launch reads the operating system’s preferred language, with English as the fallback. An explicitly saved language takes priority; changing it updates the interface and tray without restarting the proxy. Documentation and release instructions are in English.

## Inspect recent requests

In **Activity → Recent requests**, click a completed request to open its inspector. View the original client request, the request forwarded to Kilo, Kilo’s response, and the response returned to the client. Each stage includes headers and body, including tool calls and SSE data.

The last 30 requests are kept in memory only. Capture starts enabled and can be paused or cleared. Authentication headers and known keys are redacted in debug copies; arbitrary secrets inside prompts are not automatically detected. See [capture limits and security details](docs/security-and-debugging.md).

## Track observed spend

**Activity** shows reported inference costs in USD, input/output/cache tokens, and a conversation breakdown. Reported provider costs, including BYOK requests, can differ from the organization's Kilo charge; each request identifies the selected cost source. It listens to responses passing through the proxy, including streaming, and keeps totals independently of the last 30 debug captures. Codex task IDs and Claude Code session IDs are used when present; requests without an identifier are marked unassigned.

**Cache reuse** shows tokens read from cache, tokens written to cache, and the share of input reused. Conversation details include cumulative figures and the last request’s cache read / total input. Ratios use complete, comparable usage records; missing cache data is never treated as zero.

Costs that are missing stay **Not reported**, with coverage shown alongside the total. A partially reported amount is labeled **Reported subtotal**; unknown requests are never assumed to be free. Provider and gateway price fields are alternatives and are never added together for one request. A canceled response may not deliver final billing data. Totals cover all observed conversations and image calls since the current Kilo Proxy process opened. They survive hiding the window, stopping/restarting the proxy, changing organizations and clearing debug captures; quitting the process resets them. In spend-display mode, the tray shows `<$0.01` for a positive sub-cent amount, `—` when no costs were reported, and `*` for a partial subtotal. These are gateway observations, not a Kilo invoice or catalog estimate. See [accounting fields and limits](docs/security-and-debugging.md#passive-spend-tracking-0130).

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

Build outputs go to `dist/` and are excluded from Git. `VERSION` is the single default version source for the application and packages. A matching version tag runs the reusable validation workflow and publishes six verified archives with checksums and generated notes. Packaging waits for the core, browser and native jobs, packages their tested executables, and checks the extracted archives before publication. It uses the repository’s built-in `GITHUB_TOKEN`; no personal release token is required.

See [the release guide](docs/releases.md) for the exact commands, prereleases, recovery, and verification.

## Compatibility and validation

Automated tests cover authentication replacement, host/origin restrictions, lifecycle and cancellation, login states, catalog normalization, schema adaptation, streaming, trace redaction, and client configuration helpers. They use simulated credentials and gateways. Native control tests cover shared-library restart/recovery/conflicts, cross-agent propagation, folder persistence, preparation-to-launch failure guards and wide/compact English/Spanish layouts. Tray tests cover saved display preferences and complete, partial or missing process-session costs. Executable smoke checks exercise the native app separately from browser-mode Playwright tests. Cross-compilation alone does not prove native credential-store or tray behavior, and tests do not perform paid model inference. Consult the checks for a specific commit or pull request for actual results.

Codex catalog loading and reasoning/display-name metadata were checked against the installed app-server. Desktop isolation depends partly on version-specific application behavior: see [inspection notes](docs/codex-desktop-compatibility.md). Kilo must support the protocol and model you choose, and your organization must permit it.

Dependency versions are pinned in `go.mod` and `go.sum`. Licensing notices are in [THIRD-PARTY-NOTICES.txt](THIRD-PARTY-NOTICES.txt); local Gio platform changes are documented alongside `third_party/gio`, and the Windows tray ABI correction alongside `third_party/fyne-systray`. The old `third_party/systray` source remains archived reference material.

See [Xcode setup](docs/xcode.md) for Chat provider registration and the dedicated Codex/Claude agent profiles.
