# Development and release process

The repository is [rosseca/kilo-proxy](https://github.com/rosseca/kilo-proxy). Like [AISI](https://github.com/rosseca/aisi), releases are triggered by pushing a version tag. Kilo Local retains its Python packager to include native macOS app bundles, Windows GUI executables, and the optional Linux application-menu installer.

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

```sh
# Build all six targets on macOS:
python3 scripts/package.py
# Build one target only:
python3 scripts/package.py --target windows/amd64
# Build several non-macOS targets on Linux or Windows:
python3 scripts/package.py --target linux/amd64 --target windows/amd64
# Verify both macOS ZIPs after extracting them with Apple's ditto:
python3 scripts/package.py --verify-macos-archives
# Launch the native macOS archive with a temporary empty profile:
python3 scripts/smoke_macos.py
```

Darwin targets require a macOS host. The packager signs each completed app bundle with an ad-hoc signature and verifies it before creating the ZIP. Go's built-in executable signature alone does not seal an app bundle's `Info.plist` and resources; distributing that incomplete signature can prevent Finder from launching the downloaded app. The ad-hoc signature ensures bundle integrity but does not identify a trusted publisher or replace Developer ID signing and notarization.

The default version comes from the root `VERSION` file. Go embeds that same file for development builds; packaging injects the selected version into the executable. The panel reads the running server’s version. `--version` can override the package version for local experiments; official releases must match `VERSION` and the pushed tag.

The default output is `dist/`. Each invocation writes a checksum manifest for the targets it built, so build all targets in one invocation when preparing a release. Version strings are `X.Y.Z`, optionally followed by `-alpha.N`, `-beta.N`, or `-rc.N`. Pre-release macOS bundle metadata uses the numeric portion; executable version and archive names retain the full version.

## Publish a release

1. Update `VERSION`, for example to `0.12.1`, and commit the tested changes to `main`.
2. Push the commit, then an annotated matching tag:

```sh
git add VERSION
git commit -m "chore: prepare v0.12.1"
git push origin main
git tag -a v0.12.1 -m "Kilo Local v0.12.1"
git push origin v0.12.1
```

The numbers above illustrate the next patch release; always use the version actually committed in `VERSION`. For the first GitHub release, the tag is `v0.12.0`.

The **Release** workflow calls **Test and package**, which:

1. Runs Go race tests, vet, module verification, every Node helper test, and Python release tests on macOS, Linux, and Windows.
2. Checks that the tag exactly matches `VERSION` before packaging.
3. Builds all six operating-system/architecture combinations on macOS with `CGO_ENABLED=0`, including ad-hoc signing and strict verification of both macOS app bundles.
4. Extracts both macOS ZIPs with Apple's `ditto` and verifies the extracted app signatures, catching missing signature resources or ZIP packaging damage before publication.
5. Launches the native macOS archive through LaunchServices with a temporary empty profile, checks that native launch finishes and the authenticated control panel reports the expected version, and verifies a clean shutdown.
6. Creates and verifies the complete SHA-256 manifest and uploads a single `release-assets` workflow artifact.

Only after those jobs pass does the publishing job receive `contents: write`. It downloads and rechecks the assets, creates a draft with GitHub-generated notes, uploads all files, and publishes it. Stable versions become the latest release. Alpha/beta/RC tags are marked as prereleases and do not replace the latest stable release. Concurrent runs of the same tag are serialized.

The repository’s built-in `GITHUB_TOKEN` is sufficient; enable GitHub Actions if your organization disables it. No personal access token, Kilo credential, signing certificate, or Homebrew token is required. Main-branch pushes and pull requests run checks and prepare downloadable workflow artifacts without publishing a release.

This automates release publishing, not installation or updating on user machines. Publisher certificate signing, macOS notarization, Homebrew distribution, Windows MSI/EXE installers, and Linux package repositories are not configured. macOS bundles have local ad-hoc integrity signatures and may still display an unidentified-developer warning. Windows users receive the standalone GUI application in ZIP for x64 and ARM64.

## Assets and verification

Each release contains:

- `kilo-local-VERSION-darwin-arm64.zip`
- `kilo-local-VERSION-darwin-amd64.zip`
- `kilo-local-VERSION-linux-amd64.tar.gz`
- `kilo-local-VERSION-linux-arm64.tar.gz`
- `kilo-local-VERSION-windows-amd64.zip`
- `kilo-local-VERSION-windows-arm64.zip`
- `SHA256SUMS.txt`

Archives include English README/documentation and third-party notices. The source is available through GitHub’s automatically generated source archives. Build output, profiles, keys, environment files, trace files, and common editor caches are ignored by Git. `.gitignore` is not a secret scanner; review the staged file list before committing.

Download all six archives alongside the manifest, then verify on Linux:

```sh
sha256sum -c SHA256SUMS.txt
```

On macOS use `shasum -a 256 -c SHA256SUMS.txt`. For an individual Windows download, compare `Get-FileHash .\kilo-local-0.12.0-windows-amd64.zip -Algorithm SHA256` with its line in the manifest. A checksum detects file corruption; it is not a publisher signature.

For local full-set verification:

```sh
python3 scripts/release.py check-tag --tag v0.12.0
python3 scripts/release.py verify-assets
```

## Failed releases and recovery

Inspect the failed job under **Actions → Release**. Tests, version mismatch, missing assets, or checksum failures prevent publishing. Correct source issues in a new commit and use a new version/tag rather than moving an existing release tag.

For a transient upload failure, rerun the failed publishing job. An existing draft can be resumed, with its draft assets replaced from the verified build. Already published releases are rejected rather than silently overwritten. If publication succeeded but the job lost its final response, confirm the existing release and its seven assets instead of retagging it.

Development and test changes should include the relevant checks; native tray and credential-store changes also need manual validation on the affected operating system. Do not add real login credentials, administrative panel URLs, or captured conversations to issues or tests.
