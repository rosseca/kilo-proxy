# Kilo Proxy

Use your organization’s Kilo credits in your preferred coding tools. Kilo Proxy is a local proxy with a native desktop app: sign in, choose your organization and models, then open an installed agent such as Codex, Claude Code, OpenCode or Synara. It handles the organization connection, shares your model settings and prepares separate agent profiles.

Desktop is available for macOS, Windows and Linux; a separate headless edition runs from the terminal or on a server. You can also connect a [ChatGPT subscription experimentally](docs/chatgpt-subscription.md). Kilo Proxy is an independent companion, not an official Kilo product.

## Install

### Ubuntu: APT

Supports **Ubuntu 24.04 and 26.04 LTS**, on **x64 (`amd64`) and ARM64**. Add the signed repository once, then install either or both packages.

<details>
<summary>First time: add the APT repository</summary>

Download the public key and check its fingerprint:

```sh
curl -fsSLo /tmp/kilo-proxy-archive-keyring.asc \
  https://rosseca.github.io/kilo-proxy/apt/kilo-proxy-archive-keyring.asc
gpg --show-keys --with-fingerprint /tmp/kilo-proxy-archive-keyring.asc
```

It must match **`15AAF121DBFB06322EF1D7C8099722B1E76932AC`**. Once it matches:

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

</details>

```sh
# Desktop app
sudo apt-get install kilo-proxy-desktop

# Terminal / server edition
sudo apt-get install kilo-proxy-headless
```

Open **Kilo Proxy** from the applications menu or run `kilo-proxy` as your normal user. For headless, run `kilo-proxy-headless help`. Updates arrive through APT. [Full Ubuntu guide](docs/ubuntu-apt.md).

### macOS and Linux: Homebrew

With [Homebrew](https://brew.sh) installed, choose the package you need:

```sh
# macOS desktop (Apple Silicon / Intel, macOS 12+)
brew install --cask rosseca/tap/kilo-proxy

# Linux desktop (x64 / ARM64)
brew install rosseca/tap/kilo-proxy-desktop

# macOS or Linux, without a graphical interface
brew install rosseca/tap/kilo-proxy-headless
```

Open **Kilo Proxy** from Applications on macOS, or run `kilo-proxy` on Linux. The Linux desktop formula builds from source and needs a graphical session. For headless, run `kilo-proxy-headless help`. [Homebrew updates and setup](https://github.com/rosseca/homebrew-tap#kilo-proxy).

### Windows and manual downloads

Download the archive for your system and processor from the [latest release](https://github.com/rosseca/kilo-proxy/releases/latest), then extract it. All editions below support **x64 and ARM64**.

| System | Desktop | Terminal / server |
| --- | --- | --- |
| macOS 12+ | ZIP: move **Kilo Proxy.app** to Applications and open it | Headless TAR.GZ |
| Windows | ZIP: open **Kilo Proxy.exe** | — |
| Linux | TAR.GZ: run `./kilo-proxy`; optional `./install-user.sh` adds a menu entry | Headless TAR.GZ |

Release binaries need no Go, Node or Docker. Linux Desktop needs [system graphics libraries](docs/desktop.md#runtime-requirements). Each release includes `SHA256SUMS.txt` for [download verification](docs/releases.md#release-files). macOS builds are not Developer ID signed or notarized, and Windows builds are unsigned, so the system may show an origin warning.

## First launch

1. **Connect your account:** choose your Kilo organization, connect ChatGPT, or add a LiteLLM gateway.
2. **Choose models:** start with the recommended models or a curated pack, then adjust the shared library.
3. **Install or open your agent:** on macOS and Linux, **Harnesses** can check versions and install or update a private Kilo copy of supported tools. Choose the stable or nightly channel for T3 Code, or stable or Beta for Synara. Start the proxy and open your agent here. Keep Kilo Proxy running while you use it. [Supported packages and requirements](docs/managed-tools.md).

In **Settings → Accounts → Add LiteLLM**, enter your gateway's API URL and choose an API key or browser sign-in. Kilo Proxy checks the model list before saving the connection. Kilo and LiteLLM can be active together or independently. The shared catalog shows the connection for each model, and requests stay on the selected connection. [LiteLLM setup and client compatibility](docs/desktop.md#litellm).

For the terminal edition, follow the [headless setup guide](docs/headless.md).

External applications can generate embeddings through the same local API using
the saved Kilo organization connection. See the [embedding API and headless
discovery guide](docs/embeddings.md).

**Activity → History / CSV** retrieves historical Kilo usage by UTC day and up to three dimensions, including model, provider and project. Filter a date range and save raw CSV rows with exact token counts and microdollar costs. [Historical usage and CSV limits](docs/billing.md#historical-queries-and-raw-csv).

[Muse Code](docs/muse-code.md) can use Kilo's shared models or its own Meta account in the terminal and as an optional T3 provider. Keep Kilo Proxy open for Kilo connections; `kilo-muse` also works with the app closed in native Meta mode. Muse is not yet available as a Synara provider.

[Desktop guide](docs/desktop.md) · [Agents and editors](docs/clients.md) · [Models](docs/shared-models.md) · [Security](docs/security-and-debugging.md) · [Development and releases](docs/releases.md)
