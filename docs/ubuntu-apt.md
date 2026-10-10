# Ubuntu APT packages

The APT distribution is prepared for Ubuntu **24.04 LTS (noble)** and **26.04 LTS (resolute)**, on **amd64** and **arm64**. It is disabled until the repository's Pages site and dedicated APT signing identity have been configured. The commands below apply after activation; the GitHub release archives remain available independently.

Two packages can coexist:

| Package | Installed command | Use |
| --- | --- | --- |
| `kilo-proxy-desktop` | `kilo-proxy` | Native window, system tray and desktop credential storage |
| `kilo-proxy-headless` | `kilo-proxy-headless` | Console/server operation with its separate private profile |

APT installs the declared system dependencies. Desktop requires a graphical session, session D-Bus and a working graphics driver; tray icons also require desktop-shell support. Installing a package does not sign in, create or change an account, install terminal wrappers, enable a service, or launch the application. See [Desktop](desktop.md), [headless setup](headless.md) and [terminal commands](terminal-commands.md).

## Add the repository once

The planned public repository URL is `https://rosseca.github.io/kilo-proxy/apt`. Before activation, the maintainer must publish the complete signing fingerprint in this document and verify it against the deployed public key. Do not skip that comparison or use `trusted=yes`, `apt-key` or unauthenticated-package options.

After activation, download and inspect the public key:

```sh
curl -fsSLo /tmp/kilo-proxy-archive-keyring.asc \
  https://rosseca.github.io/kilo-proxy/apt/kilo-proxy-archive-keyring.asc
gpg --show-keys --with-fingerprint /tmp/kilo-proxy-archive-keyring.asc
```

Once its fingerprint matches the maintainer's published fingerprint, install the key and repository entry:

```sh
sudo install -m 0644 /tmp/kilo-proxy-archive-keyring.asc \
  /usr/share/keyrings/kilo-proxy-archive-keyring.asc
. /etc/os-release
case "$VERSION_CODENAME" in
  noble|resolute) ;;
  *) echo 'Supported Ubuntu releases: 24.04 and 26.04 LTS' >&2; exit 1 ;;
esac
printf 'Types: deb\nURIs: https://rosseca.github.io/kilo-proxy/apt\nSuites: %s\nComponents: main\nArchitectures: amd64 arm64\nSigned-By: /usr/share/keyrings/kilo-proxy-archive-keyring.asc\n' \
  "$VERSION_CODENAME" | sudo tee /etc/apt/sources.list.d/kilo-proxy.sources >/dev/null
sudo apt-get update
```

The `Signed-By` entry limits this key to the Kilo Proxy repository. This follows [Ubuntu's third-party APT repository guidance](https://ubuntu.com/server/docs/explanation/software/third-party-repository-usage/).

## Install and update

Choose either or both packages:

```sh
sudo apt-get install kilo-proxy-desktop
sudo apt-get install kilo-proxy-headless
```

Launch Desktop from the application menu or run `kilo-proxy` as your ordinary user. For headless, start with `kilo-proxy-headless help`; account setup and user-service installation remain explicit. Do not run the application with `sudo`.

Quit Desktop and stop an explicitly installed headless user service before upgrading:

```sh
sudo apt-get update
sudo apt-get install --only-upgrade kilo-proxy-desktop kilo-proxy-headless
```

Select only the package names you have installed. Executables stay at stable `/usr/bin` paths across upgrades. Package removal and purge do not delete the user's profiles or credentials. Explicitly installed terminal wrappers and user services are user-owned; uninstall them with Kilo Proxy before removing its package if they are no longer needed.

## Validation and publication

The existing Linux production builds remain on Ubuntu 24.04. The Debian packager freezes the exact previously tested executables, checks their native architecture and reported version, and calculates Desktop's shared-library requirements using `dpkg-shlibdeps`. Explicit dependencies cover graphics libraries loaded dynamically. Headless must have a static ELF executable. No build tools are installed as application dependencies.

Each build prepares all four `.deb` files and an APT repository signed by a disposable CI key. Four native container jobs exercise Ubuntu 24.04/26.04 × amd64/arm64: signature verification, APT installation and upgrade, installed payload/linkage checks, the existing full Desktop and headless smoke tests, and removal/purge while preserving disposable user-configuration sentinels. The upgrade fixture uses the candidate executable with an earlier package-metadata version; it verifies package-manager lifecycle, not migration from an earlier executable release. No personal accounts or paid inference are used.

After a stable GitHub release passes the complete build and publishes successfully, the optional APT job signs those tested `.deb` files with the dedicated production key and deploys an atomic Pages artifact. Prereleases are not published to APT. There is no scheduled polling, and an APT failure leaves the GitHub release and last successful Pages deployment intact. Retry the failed APT job after resolving the problem, rather than rerunning successful release publication.

The publisher authenticates the previous repository snapshot before extracting it, verifies the signed indexes and retained-file inventory, and retains immutable package files and SHA-256 index paths. It refuses to replace the same package/version/architecture with different bytes or deploy an older release. A missing history snapshot fails unless an explicit initial bootstrap is enabled and no existing APT metadata is found. Size limits fail before deployment and require deliberate archival maintenance; history is never silently discarded.

APT metadata deliberately has no `Valid-Until`: an infrequently released repository must not require periodic resigning. This means a previously valid signed index does not expire automatically. Users still verify signatures and package hashes, and maintainers must handle signing-key compromise or revocation explicitly. No package automatically changes users' trusted keys.

## Maintainer activation

Activation is separate from preparing the code. No production key or hosting change is required for PR tests.

1. Enable GitHub Pages for `rosseca/kilo-proxy` with the GitHub Actions build source. Its site must be dedicated to this repository output; the publisher deploys the complete site, including `/apt`.
2. Create a dedicated OpenPGP signing identity in a new private `GNUPGHOME`, with no personal keys. Back up the key and revocation certificate securely. Store its ASCII-armored private key only in the encrypted repository Actions secret `KILO_APT_SIGNING_KEY`; it must be usable noninteractively. Do not commit it or put it in logs.
3. Set the complete public fingerprint in repository variable `KILO_APT_FINGERPRINT`, publish that fingerprint above, and set `KILO_APT_ENABLED=true` only when ready to publish.
4. Set `KILO_APT_BOOTSTRAP=true` for the first deployment only, then remove it or set it to `false` immediately after verifying deployment. Subsequent deployments must authenticate and retain the existing repository history.
5. Restrict the `github-pages` environment to the intended stable release tags. Only the deployment job needs `pages: write` and `id-token: write`; build jobs use a read-only GitHub token and no production signing key. No SSH deploy key or personal access token is needed.

The `.github/workflows/apt-publish.yml` reusable workflow is called directly from Release after `publish`. It downloads the same-run `debian-assets` artifact, verifies the four files against `DEBIAN-SHA256SUMS.txt`, signs the repository and history snapshot in an isolated keyring, and deletes its temporary keyring on exit. Key rotation requires a planned trust transition; changing the configured fingerprint alone cannot authenticate history signed by a different key.
