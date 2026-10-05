# Kilo Proxy

Use your organization’s Kilo credits in your preferred editor. Kilo Proxy is a Go proxy with its own native desktop interface and a menu bar / system tray icon. It adds the organization header that many API clients cannot send themselves.

You can also connect a **ChatGPT subscription experimentally**, alone or alongside Kilo. Both connections use the same model library and agents: models labelled ChatGPT use subscription quota, while Kilo models keep using Kilo credit. Requests never fall back automatically to a paid connection. See [ChatGPT setup, quota and compatibility limits](docs/chatgpt-subscription.md).

Connect with your personal Kilo account, choose your organization, add models once, and open an installed agent from its card. The native interface uses Gio and operating-system graphics APIs. Windows runs from a standalone executable without a WebView2 installer or an additional UI runtime. Downloaded binaries require no Go, Node, Docker, or Electron. This is an independent companion, not an official Kilo product.

[Download the latest release](https://github.com/rosseca/kilo-proxy/releases/latest) · [Native desktop guide](docs/desktop.md) · [Shared models](docs/shared-models.md) · [Client setup](docs/clients.md) · [ChatGPT subscription](docs/chatgpt-subscription.md) · [Security and debugging](docs/security-and-debugging.md) · [Development and releases](docs/releases.md)

**Native desktop app:** releases from v0.21.0 use the native window and system tray. The browser interface remains available as an optional helper.

## Get started

1. Download the archive for your operating system and architecture from **Releases**, then extract it.
2. Open **Kilo Proxy.app** on macOS, **Kilo Proxy.exe** on Windows, or run `./kilo-proxy` on Linux.
3. The first-run guide opens automatically. For Kilo, click **Sign in with Kilo / SSO**, approve the device code, choose your team and click **Save & choose models**. **Use an API key or team ID instead** provides manual entry. Alternatively, choose **Sign in with ChatGPT**, complete its device authorization, then **Choose models**. You can connect both accounts.
4. Choose at least one model, then **Continue**. Names, order, default and supported reasoning preferences save automatically to the shared library.
5. Click **Start proxy and go to agents**, then **Open Codex** or another installed agent. Choose your project inside Codex Desktop; terminal agents and editors have their own folder picker. Supported profiles are prepared automatically from the shared library. Zed also receives its local credential automatically. Xcode retains its one-time provider setup under **Options**; choose Open Design's local CLI under **Engine settings** on its card.

Configured installations open **Agents** directly. **Start proxy** sits beside the stopped status; opening an agent also starts the proxy before launching it. If startup fails, the agent stays closed and the error appears in Kilo Proxy. Incomplete setup can be resumed with **Continue setup**.

The default API URL is `http://127.0.0.1:8877/v1`. The editor uses a randomly generated **local API key**, not your personal Kilo key or ChatGPT OAuth tokens. Enable **Remember** to save the Kilo credential in the operating system’s credential store. ChatGPT saves its own encrypted credential file with a separate encryption key in that store.

**Check gateway** retrieves the model catalog without paid inference. Catalog access does not prove organization balance or permission to generate with a model. Verify those with a request from your editor and its attribution in Kilo.

Closing the application window leaves the proxy running. The system tray / menu bar can reopen the interface, show status and observed spend, start or stop the saved connection, and quit the application. **Settings → Appearance** saves your choice of **K icon** or **Session cost**; macOS shows the K icon and amount together in **Session cost**, while other tray hosts may use the K icon, tooltip and menu. Stopping cancels active requests. The proxy does not start automatically when opening the application.

On macOS, Linux and Windows, **Settings → Terminal commands** adds `kilo-codex`, `kilo-claude`, `kilo-opencode` and `kilo-omp` together. Use **Install terminal commands** on macOS/Linux or **Install PowerShell functions** on Windows. Open a new terminal in your project and run the command for your installed CLI to use the latest saved shared models in that terminal. Arguments pass through, including `kilo-codex resume`, `kilo-claude --resume`, `kilo-opencode --continue` and `kilo-omp --resume`. Keep Kilo Proxy open, including in the tray; the commands start its saved connection when needed. Your ordinary CLI authentication is preserved. Use the install/update button again after moving the app or to add commands missing from an older installation. See [terminal command setup](docs/terminal-commands.md).

Prefer to configure your shell yourself? **Manual setup** on the same Settings page offers a separate copy button for each command and **Copy all**. Paste the generated functions into `.zshrc` or `.bashrc` on macOS/Linux, or into `$PROFILE` in Windows PowerShell 5.1 or PowerShell 7. No installer or PATH changes are needed, and the blocks contain no API keys.

## Downloads

| System | Architecture | Archive contents |
| --- | --- | --- |
| macOS | Apple Silicon / Intel | `.app` bundle in ZIP; macOS 12 or later |
| Windows | x64 / ARM64 | Standalone native GUI executable in ZIP |
| Linux | x64 / ARM64 | Native executable in TAR.GZ; system graphics libraries; optional `install-user.sh` launcher |

From 0.55.0, every release includes six desktop archives, four separate macOS/Linux headless archives, and `SHA256SUMS.txt`. Earlier releases contain the six desktop archives. Linux’s desktop installer adds an application-menu entry for the current user without administrator privileges. Linux tray support requires a graphical session with D-Bus and StatusNotifierItem/AppIndicator support; GNOME may need an AppIndicator extension. Use `--browser` for the optional browser interface. Desktop `--no-tray` disables its window and tray while retaining that build's dependencies and OS keyring; servers use the separate `kilo-proxy-headless` executable. See [desktop dependencies and launch options](docs/desktop.md) for Linux graphics requirements and [headless setup](docs/headless.md) for console operation. There are no mobile packages.

macOS bundles have an **ad-hoc signature** covering the executable, bundle metadata, and resources. They are **not Developer ID signed or notarized**; Windows binaries are unsigned. macOS and Windows may show origin warnings. Company-wide managed distribution can add publisher signing and macOS notarization separately. The desktop app does not install an auto-updater. Headless user services are installed only by an explicit CLI command.

## App updates

Kilo Proxy checks this repository's latest stable GitHub release in the background when it starts and every six hours while it remains open. A newer version shows a download notice. **Settings → App updates** shows the installed version, last check, and a manual **Check for updates** button. The optional browser interface has the same controls in **App updates**.

Checks use GitHub's public release API without your Kilo credentials, organization, conversations, or GitHub authentication. Manual checks are limited to once per minute. Offline or rate-limited checks show an unavailable status and do not stop the proxy. The download button opens the exact release page; installation stays under your control. Drafts and prereleases are excluded, and development builds with an unrecognized version do not claim to be up to date.

## Editors and models

| Client | What the helper configures |
| --- | --- |
| Codex Desktop | Separate GUI profile, multiple models, short display names, native reasoning selector |
| Codex CLI | Separate generated profile using the shared models, names, reasoning levels and terminal launcher |
| OpenCode | Automatic profile preparation, multiple models, names, limits, and scoped launcher |
| Oh My Pi | Isolated OMP terminal profile, shared models, names, supported reasoning, and Kilo image MCP |
| Open Design | Codex CLI, Claude Code or OpenCode engine, private profiles from shared models, separate desktop workspace, and automatic proxy startup |
| Claude Code | Automatic isolated profile, version-aware model picker, short names, native effort and terminal launcher |
| Claude Desktop · Kilo | Private Desktop and Code/auth profiles, official gateway configuration, shared Claude models and names, automatic desktop launch |
| OpenMausBot | Separate desktop workspace, shared model IDs and default, OpenAI-compatible connection, and automatic proxy startup |
| T3 Code · Kilo | Separate T3 workspace with Codex and Claude Code agents using either normal CLI profiles or private Kilo profiles |
| Zed | Automatic local credentials and JSONC settings updates, multiple models, names, and initial model |
| Xcode | Dedicated Chat model list, automatic Codex/Claude agent profiles, version-aware Claude aliases and setup guidance |

**Open Design uses Codex CLI, Claude Code or OpenCode through the local proxy.** Choose **Agents → Open Design → Engine settings**, select an installed engine, and click **Launch Open Design**. Kilo Proxy prepares private profiles from the shared library and starts a separate Open Design workspace on macOS or Windows, without copying credentials manually. Keep **CLI default** in Open Design for the shared default; its own model picker may not list every shared choice. See [Open Design setup and Linux guidance](docs/open-design.md).

**T3 Code · Kilo offers four agents in one separate workspace:** Codex · Normal, Kilo Proxy · Codex, Claude · Normal and Kilo Proxy · Claude. Kilo agents have a green **KP** badge on T3's provider rail and composer icons. Install T3 Code desktop 0.0.45 or the validated 0.0.46-nightly.20261003.2610 nightly, Codex CLI and Claude Code, then choose **Agents → T3 Code · Kilo → Integration settings**, prepare the shared models and open T3. Normal agents use your existing CLI sign-in; Kilo agents use private profiles and the local proxy. Stable T3 requires a new chat to switch between normal and Kilo agents; the validated nightly supports switching between turns with T3’s summary handoff. See [T3 setup, reasoning and compatibility](docs/t3-code.md).

The model helpers support catalog search, manual IDs, context metadata, and input/output prices in USD per million tokens. Prices come from Kilo’s catalog, not your invoice. Explicitly free prices show zero; variable or missing prices remain unavailable. Refreshing models does not run inference. The native **Models** library is shared by default and saved across restarts in the application configuration directory, separately from generated profiles. **Models → Packs** offers five candidate selections that can be assigned per agent; **My packs** lets you create an editable copy, add or remove catalog models, and switch packs without changing the shared library. Neither file stores API keys. See [shared-model and pack storage and compatibility](docs/shared-models.md). The optional browser helper retains its independent per-client selections.

When ChatGPT is connected, its account catalog joins this library under `chatgpt/<slug>`, with a **ChatGPT** suffix on catalog names. Existing Kilo IDs, including `openai/<slug>`, keep their original routing. Subscription models have no per-token price. The optional image-generation MCP has a separate Kilo / ChatGPT provider choice. This direct subscription backend is experimental and does not support every field of the public APIs. See [protocol and client limits](docs/chatgpt-subscription.md#protocol-and-client-limits).

Choose **Recommended** (272K tokens), **Low** (128K tokens), **Maximum**, or **Custom** context in Models. Presets save automatically and apply on the next agent preparation or launch, bounded by the model's published capacity. Existing numeric limits remain Custom until you change them. [Context presets and agent compatibility](docs/shared-models.md#context-window-presets).

Browse models in a responsive **card grid**, with names, IDs and input/output prices together. In **Models → Add models**, filter by **lab**, using publishers from your catalog and saved manual models, then sort by **Code Mode Rank**, **Coding Index**, **Speed**, **Price**, or **Name**. The default is Kilo's seven-day Code mode usage rank; price ordering uses input cost. Missing metrics appear last, and filtering and sorting preserve your selections and initial model. [Sources and sorting behavior](docs/clients.md#sort-the-model-catalog).

For Codex Desktop, edit the shared library and click **Open Codex** on **Agents** to prepare the isolated profile folder, save or update both `config.toml` and `models.json`, and open Codex with separate interface data. Unrelated settings are preserved and changed files are backed up. Close the Kilo instance before relaunching if its environment settings changed. Manual preparation and command exports remain available. The actual Kilo model IDs remain unchanged.

The Codex Desktop helper also exposes its message queue mode. **Queue** keeps new messages pending until the active turn finishes; **Steer** adds them to the running task. Preparing the profile writes `desktop.followUpQueueMode`; restart Codex Kilo before testing the new behavior. Codex CLI is unchanged.

**Images in Codex, available from v0.23.0.** **Models → Image generation** can optionally configure a `generate_image` MCP tool inside Kilo Proxy. Choose **Kilo · account credits** and an image-output model, or **ChatGPT subscription · Experimental** and its automatic built-in image tool, independently of your coding models. **Save image settings** persists the shared image setup without requiring a Codex profile; reopen an agent to apply it. This is separate from the automatic model library. Images are saved locally. Kilo image requests use the configured account and organization, with returned costs in Activity; ChatGPT image requests use subscription quota and are tracked without an invented dollar charge. The selected image provider never falls back to the other account. Provider inference costs, including BYOK, can differ from the organization's Kilo charge. No additional runtime is needed. See [image setup and editing limits](docs/codex-images.md).

From v0.23.1, image results include a preview bounded to **1024 pixels per side and 256 KiB per image**, while the full-resolution original stays saved locally for export and editing. Existing conversations can still contain large inline images; an upstream **413** may require client-side compaction, a new conversation, or fewer attachments. See [payload limits and recovery](docs/codex-images.md#payload-limits-and-413-errors).

**Settings → Large images** handles oversized Responses, Chat Completions and Anthropic Messages requests. New profiles default to **Cloudflare quick tunnel**. You can also choose **Off**, **Compress locally**, **Upload to Kilo · Experimental**, **Litterbox**, or **Tailscale Funnel**; existing saved choices are preserved. Compression has fixed **High quality**, **Balanced**, and **Small size** profiles. URL modes preserve the image bytes; local originals stay unchanged in every mode. Cloudflare and Tailscale require their installed executables and expose only a separate temporary image server. When Cloudflare is selected and `cloudflared` is missing, startup shows installation guidance and a check-again action; the proxy can still start for ordinary requests. Litterbox needs no extra executable, but uploads to a third party with a chosen expiry and no early deletion. Modes never fall back to another service automatically. Read [setup, limits, and image lifetime](docs/image-uploads.md).

The Claude Code card detects the installed version, prepares a separate profile with backups, and opens an interactive terminal. Shared names and reasoning preferences apply only where that version and model support them. See [client setup](docs/clients.md) for profile isolation, saving, and compatibility limits.

**Claude Desktop · Kilo** has its own full-width row immediately below Codex. **Open Claude Desktop · Kilo** prepares a private Desktop and Code/auth profile from the shared library, without copying ordinary Claude's history or login. Ordinary Claude can stay open and launches from its usual icon; quit only the Kilo instance before reopening it or changing its profile. Claude models work directly; **Integration settings → Experimental: use models from other providers** enables local aliases for other Kilo models while preserving their real display names and cost attribution. See [profile storage, restoration for earlier users and compatibility limits](docs/claude-desktop.md).

**OpenMausBot** has a full-width row below Codex and Claude Desktop. **Open OpenMausBot** configures its OpenAI-compatible engine from the shared library and opens a separate workspace. Both Kilo and `chatgpt/` models use the local proxy, which applies the prepared reasoning level for each model. Its current engine shows exact model IDs and has no native reasoning selector. See [setup, profile isolation and compatibility](docs/openmausbot.md).

OpenCode and Zed receive the shared IDs, names, default and token limits through JSONC-preserving updates. Their reasoning remains automatic. OpenCode includes local authentication in its dedicated profile; Zed receives its local key in the system credential store and refreshes the provider when the key changes, including in an already-open editor. See [their setup guide](docs/opencode-and-zed.md).

**Oh My Pi** opens the installed `omp` terminal agent with a dedicated `~/.omp-kilo` profile. It uses the shared models, short names, default and supported reasoning over Responses. The local image MCP is configured when image generation is enabled. See [Oh My Pi setup and compatibility](docs/oh-my-pi.md).

The application and tray support **English / Español**. The first launch reads the operating system’s preferred language, with English as the fallback. An explicitly saved language takes priority; changing it updates the interface and tray without restarting the proxy. Documentation and release instructions are in English.

## Inspect recent requests

In **Activity → Recent requests**, click a completed request to open its inspector. View the original client request, the request forwarded to Kilo, Kilo’s response, and the response returned to the client. Each stage includes headers and body, including tool calls and SSE data.

Request capture is **off by default**, including for existing installations upgrading without an explicit preference. Enable **Capture request details** in Activity when debugging; the choice is saved between launches. When enabled, the last 30 requests are kept in memory only. Turning capture off immediately erases retained requests and cancels in-flight captures; cost and token accounting continues independently. Authentication headers and known keys are redacted in debug copies; arbitrary secrets inside prompts are not automatically detected. See [capture limits and security details](docs/security-and-debugging.md).

## Track observed spend

**Activity → Kilo account** shows the remaining shared team balance and your billed usage today, yesterday and over the last 30 days, fetched from Kilo. **Settings → Appearance → Kilo balance** can display the balance beside the K icon. A separate private `usage-history.json` automatically saves local daily aggregates across restarts, even with request capture disabled. Remote account charges and locally observed inference costs are labeled separately. See [balance, usage history, privacy and API limits](docs/billing.md).

**Subscription usage** separately shows ChatGPT quota windows and reset times. **Settings → Appearance → ChatGPT quota** displays the remaining percentage of its primary window; unavailable or stale data shows `—`. Subscription requests keep their token counts and local history without being treated as monetary charges or missing Kilo prices. See [quota, tokens and money](docs/chatgpt-subscription.md#quota-tokens-and-money).

**Activity** shows reported inference costs in USD, input/output/cache tokens, and a conversation breakdown. Reported provider costs, including BYOK requests, can differ from the organization's Kilo charge; each request identifies the selected cost source. It listens to responses passing through the proxy, including streaming, and keeps totals independently of the last 30 debug captures. Codex task IDs and Claude Code session IDs are used when present; requests without an identifier are marked unassigned.

**Cache reuse** shows tokens read from cache, tokens written to cache, and the share of input reused. Conversation details include cumulative figures and the last request’s cache read / total input. Ratios use complete, comparable usage records; missing cache data is never treated as zero.

Costs that are missing stay **Not reported**, with coverage shown alongside the total. A partially reported amount is labeled **Reported subtotal**; unknown requests are never assumed to be free. Provider and gateway price fields are alternatives and are never added together for one request. A canceled response may not deliver final billing data. Totals cover all observed conversations and image calls since the current Kilo Proxy process opened. They survive hiding the window, stopping/restarting the proxy, changing organizations and clearing debug captures; quitting the process resets them. In spend-display mode, the tray shows `<$0.01` for a positive sub-cent amount, `—` when no costs were reported, and `*` for a partial subtotal. These are gateway observations, not a Kilo invoice or catalog estimate. See [accounting fields and limits](docs/security-and-debugging.md#passive-spend-tracking-0130).

## Console and server use

The macOS/Linux `kilo-proxy-headless` executable runs without a desktop or tray, with a separate private configuration profile. Configure Kilo or ChatGPT by device login, select shared models and reasoning from the CLI, then run `serve` and use `kilo-codex`, `kilo-claude`, `kilo-opencode`, or `kilo-omp`. Setup and startup suggest the exact command to install these wrappers on your machine, including a custom profile path; installation is explicit and reports how to activate your shell. Each headless profile keeps its agent configuration separate. Its file credential vault uses private files instead of the desktop OS keyring. See [headless setup, server operation, and credential storage](docs/headless.md).

## Develop and release

Requirements: Go 1.26 or later, Python 3.9 or later for packaging, and Node 22 or later for helper tests. Runtime users do not need these tools.

```sh
go mod download
go run -tags desktop .
```

Desktop builds require platform development tools on macOS/Linux; Windows uses its built-in graphics APIs. See [native build instructions](docs/desktop.md#building-locally). For the optional browser interface, run `go run . --browser`.

For an isolated headless development profile:

```sh
CGO_ENABLED=0 go run -tags headless . --config-dir ./tmp-headless-profile serve --setup
```

This runs the console controller without graphics libraries or a desktop credential store. Configure it from another terminal using the same profile. Headless startup prints setup guidance; its private control descriptor stays inside that profile. Browser mode prints a panel URL containing an administrative token, which must be kept private.

```sh
go test -race ./...
go vet ./...
node --test scripts/*.test.mjs
python3 -m unittest discover -s scripts -p 'test_*.py'
# On a host with native graphics dependencies:
go test -tags desktop ./...
python3 scripts/package.py --build-only
```

Build outputs go to `dist/` and are excluded from Git. `VERSION` is the single default version source for the application and packages. A matching version tag runs the reusable validation workflow and publishes ten verified archives from 0.55.0 with checksums and generated notes. Packaging waits for the core, browser, native desktop, and headless jobs, packages their tested executables, and checks the extracted archives before publication. It uses the repository’s built-in `GITHUB_TOKEN`; no personal release token is required.

See [the release guide](docs/releases.md) for the exact commands, prereleases, recovery, and verification.

## Compatibility and validation

Automated tests cover authentication replacement, host/origin restrictions, lifecycle and cancellation, login states, catalog normalization, schema adaptation, streaming, trace redaction, and client configuration helpers. They use simulated credentials and gateways. Native control tests cover shared-library restart/recovery/conflicts, cross-agent propagation, folder persistence, preparation-to-launch failure guards and wide/compact English/Spanish layouts. Tray tests cover saved display preferences and complete, partial or missing process-session costs. Executable smoke checks exercise the native app separately from browser-mode Playwright tests. Cross-compilation alone does not prove native credential-store or tray behavior, and tests do not perform paid model inference. Consult the checks for a specific commit or pull request for actual results.

Codex catalog loading and reasoning/display-name metadata were checked against the installed app-server. Desktop isolation depends partly on version-specific application behavior: see [inspection notes](docs/codex-desktop-compatibility.md). Kilo must support the protocol and model you choose, and your organization must permit it.

ChatGPT tests cover synthetic OAuth, encrypted storage, concurrent refresh, account isolation and protocol/tool conversion. Installed-client checks have also verified real subscription tool turns in Codex CLI, Claude Code, OpenCode and Oh My Pi using temporary profiles. Live checks require explicit opt-in and consume subscription quota; CI uses synthetic servers and sanitized client fixtures. See [the exact validation scope and live-test instructions](docs/chatgpt-subscription.md#validation-scope).

Dependency versions are pinned in `go.mod` and `go.sum`. Licensing notices are in [THIRD-PARTY-NOTICES.txt](THIRD-PARTY-NOTICES.txt); local Gio platform changes are documented alongside `third_party/gio`, and the Windows tray ABI correction alongside `third_party/fyne-systray`. The old `third_party/systray` source remains archived reference material.

See [Xcode setup](docs/xcode.md) for Chat provider registration and the dedicated Codex/Claude agent profiles.
