# Development and release process

The repository is [rosseca/kilo-proxy](https://github.com/rosseca/kilo-proxy). Like [AISI](https://github.com/rosseca/aisi), releases are triggered by pushing a version tag. The desktop application uses a native Gio window and system tray. From 0.55.0, releases also include a separate pure-Go headless executable for macOS and Linux servers. Kilo Proxy retains its Python packager for macOS app bundles, Windows GUI executables, headless archives, and the optional Linux application-menu installer. Gio is pinned to `v0.10.2`, with documented [virtual desktop compatibility patches](../third_party/gio/PATCHES.md). Its controls render directly through operating-system graphics APIs; no embedded browser or WebView runtime is required. Changing the framework version requires the same native checks as an application change.

## Local checks

Use Go 1.26 or later, Python 3.9 or later, and Node 22 or later. Use the latest patched Go toolchain compatible with `go.mod` for distribution builds.

```sh
go mod download
go mod verify
go test -race ./...
go vet ./...
node --test scripts/*.test.mjs
python3 -m unittest discover -s scripts -p 'test_*.py'
```

Run the Node glob from a shell that expands it, such as Bash or zsh. CI uses Bash on all three operating systems.

When changing T3 Code profiles or model selection, also run the installed-client acceptance against each supported Desktop release:

```sh
KILO_TEST_T3_APP='/absolute/path/to/T3 Code.app' KILO_TEST_T3_PICKER=1 go test -race -run '^TestT3CodeInstalledFourAgents$' -count=1 .
```

This macOS opt-in check requires installed Codex CLI, Claude Code and Playwright Chromium. It uses disposable homes and synthetic local upstreams, reproduces an old built-in Claude selection, and verifies the exact gateway ID through the packaged frontend and native drivers. Desktop persistence/bootstrap IPC is injected into headless Chromium; this is not a native Electron IPC acceptance test. Existing application windows and account profiles are not used.

When changing Synara profiles or account/model selection, run the installed-client acceptance against the Beta build being validated:

```sh
go build -o /private/tmp/kilo-synara-adapter .
KILO_TEST_SYNARA_APP='/absolute/path/to/Synara Beta.app' KILO_TEST_SYNARA_ADAPTER_SOURCE=/private/tmp/kilo-synara-adapter KILO_TEST_SYNARA_PICKER=1 go test -race -run '^TestSynaraInstalledFourAgents$' -count=1 .
```

This macOS opt-in check requires installed Codex CLI, Claude Code and Playwright Chromium. It uses disposable homes and synthetic local upstreams to exercise the four accounts, the packaged account/model picker and native CLI drivers. Desktop persistence/bootstrap IPC is injected into headless Chromium; this does not verify the native Electron GUI or its IPC. Existing application windows and account profiles are not used.

Use the same app and compiled-adapter variables with `-run '^TestSynaraInstalledCodexVoiceHealth$'` to check real CLI login-status parsing with synthetic ChatGPT, API-key and unsigned-in profiles. It records no microphone audio and requests no inference. The private backend integration points are checked before applying the account-aware delegation correction. Synara Beta versions are not restricted to an allowlist; installed-client acceptance establishes evidence for the tested build.

Add `KILO_TEST_SYNARA_DESKTOP=1` to the same command for a separate native Electron startup check. It uses the production launch plan with disposable storage, verifies the private backend and native snapshot import, then sends SIGTERM only to its own process, confirms a quit dialog if present and checks graceful shutdown. A temporary loopback debugging connection controls only that owned window's quit button; this does not establish native GUI inference coverage.

When changing Codex context presets, also check the installed client's actual model switching and auto-compaction against a disposable synthetic gateway:

```sh
python3 scripts/check-codex-context.py /absolute/path/to/codex
```

This creates an isolated temporary profile, verifies Recommended/Low usable windows and an automatic compaction event, then removes the profile. It does not contact Kilo or use personal credentials. CI covers preset persistence, exported metadata and native/browser interactions without requiring an installed third-party agent.

When changing the Codex V1 collaboration compatibility repair, run the installed-client A/B check:

```sh
python3 scripts/check-codex-collab.py /absolute/path/to/codex
```

The script uses two disposable profiles and a local synthetic SSE gateway. It verifies that `multi_agent_v1.spawn_agent` rejects a nonempty `message` together with `items: []`, then creates a subagent when the otherwise identical arguments omit `items`. Success requires a completed collaboration event containing the returned child agent ID. Both temporary Codex processes are stopped and their profiles removed, including when an assertion fails. No existing profiles or agents are used and no paid inference is requested.

This A/B check connects Codex directly to the synthetic gateway; it does **not** test the full Codex → Kilo Proxy network path. The Go collaboration bridge tests separately cover the corresponding argument repair in JSON and streamed SSE responses, including cases that must remain unchanged. Run both checks when modifying the repair.

The desktop build additionally requires Xcode command line tools on macOS, and X11/Wayland, EGL and Vulkan development libraries on Linux. For Ubuntu 24.04:

```sh
sudo apt-get install build-essential pkg-config libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-x11-dev libxcursor-dev libxfixes-dev libegl1-mesa-dev libgles2-mesa-dev libvulkan-dev mesa-vulkan-drivers libgl1-mesa-dri xvfb dbus-x11
npm ci
npx playwright install --with-deps chromium webkit
npm run test:e2e
```

The Linux desktop runtime uses normal X11/Wayland and graphics system libraries; CI builds on Ubuntu 24.04 for x64 and ARM64. Windows executables use the system's Direct3D 11 hardware or built-in WARP implementation, without a UI runtime installer. macOS 12+ uses the built-in Metal graphics API. The tray uses Cocoa, the Windows notification area, or the Linux StatusNotifierItem DBus protocol; Linux desktops need a compatible tray host to display its icon.

Build and test the native target first. For example, on Apple Silicon:

```sh
python3 scripts/package.py --build-only --target darwin/arm64 --output dist/binaries
python3 scripts/smoke_desktop.py --binary dist/binaries/kilo-proxy-darwin-arm64
python3 scripts/package.py --binaries-directory dist/binaries --target darwin/arm64
python3 scripts/package.py --verify-macos-archives --target darwin/arm64
python3 scripts/smoke_macos.py
```

`--build-only` emits `kilo-proxy-OS-ARCH` (plus `.exe` on Windows), using `go build -tags desktop`. Windows uses `CGO_ENABLED=0`; macOS and Linux use `CGO_ENABLED=1`. Linux builds require a native host of the target architecture; macOS can build both Darwin architectures; Windows can be cross-compiled with Go. The release workflow uses native hardware for all six targets so each executable can also be launched and tested.

With no `--target`, local builds select the host architecture. `--binaries-directory` selects all six supplied binaries unless a target is specified. It inspects each executable's native header before packaging, rejects missing files or incorrect CPU/OS artifacts, and checks Windows imports for non-system DLL dependencies. It never recompiles the tested input. To aggregate archives downloaded from the platform jobs:

```sh
python3 scripts/package.py --checksums-only
python3 scripts/release.py verify-assets
```

`--checksums-only` requires all ten archives from 0.55.0, or the historical six desktop archives for older versions, before replacing the manifest. Each individual packaging invocation writes a manifest only for its selected targets. The final aggregation step creates the complete release manifest.

Headless builds use `CGO_ENABLED=0 go build -tags headless` and can be cross-compiled without graphics development libraries. They emit `kilo-proxy-headless-OS-ARCH`; the macOS/Linux x64 and ARM64 TAR.GZ archives contain the console executable, [headless setup guide](headless.md), README and third-party notices. Build and smoke-test on the native target before packaging the same executable:

```sh
python3 scripts/package.py --headless --build-only --target linux/amd64 --output dist/headless-binaries
python3 scripts/smoke_headless.py --binary dist/headless-binaries/kilo-proxy-headless-linux-amd64
python3 scripts/package.py --headless --binaries-directory dist/headless-binaries --target linux/amd64
python3 scripts/smoke_headless.py --archive dist/kilo-proxy-headless-0.55.0-linux-amd64.tar.gz
```

The headless smoke uses a disposable home and private configuration with no display or desktop credential bus. It checks the native binary header (static ELF on Linux; no macOS graphics frameworks), stdin-based credential setup, model import/export, file permissions, authenticated runtime, exclusive profile ownership, all four terminal wrappers with synthetic agents, normal-profile preservation, signal cleanup and restart. It does not contact a paid upstream or exercise real agent inference. User service definitions are covered with injected service managers; tests do not install services on the development machine.

Darwin bundles must be packaged on macOS. The packager signs each completed app bundle with an ad-hoc signature and verifies it before creating the ZIP. Go's executable signature alone does not seal an app bundle's `Info.plist` and resources; distributing that incomplete signature can prevent Finder from launching the downloaded app. The ad-hoc signature ensures bundle integrity but does not identify a trusted publisher or replace Developer ID signing and notarization.

The default version comes from the root `VERSION` file. Go embeds that same file for development builds; the raw-binary build injects the selected version. The panel reads the running server's version. `--version` can override the version for local experiments; official releases must match `VERSION` and the pushed tag. When supplying prebuilt binaries, keep the build and package version identical.

The default output is `dist/`. Version strings are `X.Y.Z`, optionally followed by `-alpha.N`, `-beta.N`, or `-rc.N`. Prerelease macOS bundle metadata uses the numeric portion; executable version and archive names retain the full version.

## Publish a release

Merge the reviewed and tested changes before preparing a release. The matching version tag triggers the full validation and publication workflow below.

1. Update `VERSION`, for example to `0.22.0`, and commit the tested changes to `main`.
2. Push the commit, then an annotated matching tag:

```sh
git add VERSION
git commit -m "chore: prepare v0.22.0"
git push origin main
git tag -a v0.22.0 -m "Kilo Proxy v0.22.0"
git push origin v0.22.0
```

The numbers above are examples; always use the version actually committed in `VERSION`. The historical first GitHub release was `v0.12.0`, published under the former Kilo Local name.

The **Release** workflow calls **Test and package**, which:

1. Runs Go race tests, vet, module verification, every Node helper test, and Python release tests on macOS, Linux, and Windows. Release tags must match `VERSION`.
2. Runs Playwright E2E in separate Chromium and WebKit jobs against temporary profiles and a synthetic gateway. Each browser keeps one worker and its own time budget; failures retain reports and traces in browser-specific CI artifacts.
3. Runs the Go native control tests, including shared-library restart/recovery/conflict/propagation, Agents preparation-to-open and folder persistence, and saved tray/spend behavior. It renders Agents and Models review screenshots at wide/compact sizes in English and Spanish on all six targets, then builds raw production desktop executables on native Apple Silicon, Intel Mac, Linux x64/ARM64, and Windows x64/ARM64 runners. These jobs create no app bundles or release archives.
4. Runs the native desktop self-test on every target: rendered Agents/Models/Activity/Settings controls, shared-model save/reload and in-memory client propagation, authenticated backend access, language handling, native clipboard, closing and reopening the window through the tray action, continued proxy operation with the window closed, persisted tray appearance and process-session spend presentation, and graceful quit. Each invocation explicitly selects a temporary test profile and synthetic data; it never needs real Kilo credentials or billed requests.
5. Runs headless race tests, pure-Go vet, production builds and console smoke on native macOS and Linux x64/ARM64 runners. These jobs install no desktop session or graphics helpers.
6. Only after **all** core, browser, desktop and headless tests pass, packages the previously tested executables. No rebuild occurs between the native test and packaging. Both macOS desktop bundles receive complete ad-hoc signatures and strict integrity checks.
7. Extracts and reruns the native desktop self-test from every desktop release archive, and the headless smoke from all four console archives. Both macOS ZIPs also receive signature verification and an additional LaunchServices launch check to detect bundle or startup regressions.
8. Downloads all ten archives, creates and verifies the complete SHA-256 manifest, and uploads the single `release-assets` workflow artifact consumed by the publishing job. The ordinary validation run has 22 jobs; Release adds the publishing job for 23.

Only after those jobs pass does the publishing job receive `contents: write`. It downloads and rechecks the assets, creates a draft with GitHub-generated notes, uploads all files, and publishes it. Stable versions become the latest release. Alpha/beta/RC tags are marked as prereleases and do not replace the latest stable release. Concurrent runs of the same tag are serialized.

The repository’s built-in `GITHUB_TOKEN` is sufficient; enable GitHub Actions if your organization disables it. No personal access token, Kilo credential, signing certificate, or Homebrew token is required. Main-branch pushes and pull requests run checks and prepare downloadable workflow artifacts without publishing a release.

This automates release publishing, not installation or updating on user machines. Publisher certificate signing, macOS notarization, Homebrew distribution, Windows MSI/EXE installers, and Linux package repositories are not configured. macOS bundles have local ad-hoc integrity signatures and may still display an unidentified-developer warning. Windows users receive the standalone GUI application in ZIP for x64 and ARM64.

## Assets and verification

### Windows ARM64 hosted-runner shell preparation

The Windows native and package jobs first run an independent Win32 notification probe, using Python's standard-library `ctypes`. It records the actual process architecture, structure layout, window validity and notification registration results. This distinguishes an application failure from a runner shell that rejects ordinary `Shell_NotifyIconW(NIM_ADD)` calls.

Some hosted Windows ARM64 images leave a first-login privacy/OOBE host active. The image issue is tracked in [actions/runner-images #14069](https://github.com/actions/runner-images/issues/14069); a related application documents the notification-area effect in [Tiny Clips #304](https://github.com/jamesmontemagno/tiny-clips/pull/304). Our independent probe reproduced the failure in native ARM64 Python with valid windows and notification structures, while equivalent Windows x64 registration worked.

Recovery is a CI fixture restricted to GitHub-hosted Windows ARM64, a failed production-equivalent probe, and a known OOBE host in the runner's own interactive session. It stops only allowlisted OOBE hosts and restarts only that session's Explorer, then requires a successful native registration with a real icon, full structure size and callback. Recovery rejects local, self-hosted or non-ARM64 environments. Guard tests exercise these boundaries, and before/after reports remain CI artifacts. The production application never runs this fixture or modifies Explorer. All native lifecycle checks and extracted-archive tests remain mandatory after shell preparation.

The guarded fixture first sets Microsoft's supported [DisablePrivacyExperience user policy](https://learn.microsoft.com/en-us/windows/client-management/mdm/policy-csp-privacy#disableprivacyexperience) in the disposable runner's `HKCU` to prevent the observed privacy host from relaunching. No machine-wide policy is changed. This policy prevents new launches; it does not itself guarantee dismissal of an already active flow, so the subsequent independent registration must still succeed. Failed probes can include a desktop screenshot captured using built-in GDI, restricted to GitHub-hosted Windows jobs, to make any remaining OOBE obstruction reviewable.

### Release files

Each release from 0.55.0 contains ten archives and their manifest:

- `kilo-proxy-VERSION-darwin-arm64.zip`
- `kilo-proxy-VERSION-darwin-amd64.zip`
- `kilo-proxy-VERSION-linux-amd64.tar.gz`
- `kilo-proxy-VERSION-linux-arm64.tar.gz`
- `kilo-proxy-VERSION-windows-amd64.zip`
- `kilo-proxy-VERSION-windows-arm64.zip`
- `kilo-proxy-headless-VERSION-darwin-arm64.tar.gz`
- `kilo-proxy-headless-VERSION-darwin-amd64.tar.gz`
- `kilo-proxy-headless-VERSION-linux-amd64.tar.gz`
- `kilo-proxy-headless-VERSION-linux-arm64.tar.gz`
- `SHA256SUMS.txt`

Archives include English README/documentation and third-party notices. The source is available through GitHub’s automatically generated source archives. Build output, profiles, keys, environment files, trace files, and common editor caches are ignored by Git. `.gitignore` is not a secret scanner; review the staged file list before committing.

Older releases contain the six desktop archives and their manifest. Download all archives listed in the manifest alongside it, then verify on Linux:

```sh
sha256sum -c SHA256SUMS.txt
```

On macOS use `shasum -a 256 -c SHA256SUMS.txt`. For an individual Windows download, compare `Get-FileHash .\kilo-proxy-0.22.0-windows-amd64.zip -Algorithm SHA256` with its line in the manifest. A checksum detects file corruption; it is not a publisher signature.

For local full-set verification:

```sh
python3 scripts/release.py check-tag --tag v0.22.0
python3 scripts/release.py verify-assets
```

## Failed releases and recovery

Inspect the failed job under **Actions → Release**. Tests, version mismatch, missing assets, or checksum failures prevent publishing. Correct source issues in a new commit and use a new version/tag rather than moving an existing release tag.

For a transient upload failure, rerun the failed publishing job. An existing draft can be resumed, with its draft assets replaced from the verified build. Already published releases are rejected rather than silently overwritten. If publication succeeded but the job lost its final response, confirm the existing release and its expected archives plus manifest instead of retagging it.

Development and test changes must include the relevant automated checks. Native window and tray changes must pass every platform smoke job before bundling. Changes to real SSO or credential-store behavior also need a manual account-based check, since CI deliberately uses synthetic data. Do not add real login credentials, administrative panel URLs, or captured conversations to issues or tests.
