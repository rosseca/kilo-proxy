# Muse Code

Kilo Proxy opens [Muse Code](https://dev.meta.ai/products/muse-code) in a project terminal. In **Harnesses → Muse Code → gear**, choose **Kilo Proxy · shared models** or **Own Meta account**. Kilo is the default connection for this harness. A compatible installed Muse CLI is required for the Kilo connection; an unsupported selected installation produces an explicit error rather than opening a Meta login or substituting another executable.

## Installation and executable selection

Open **Harnesses → Muse Code** to check the installation. On macOS and Linux, **Install or update** offers Kilo's private copy from Meta's official release manifest. Kilo verifies the download size, SHA-256, operating system, architecture and version before activating it. On Windows, install the official executable manually.

In **Settings → CLI installations**, the optional **Muse Code executable path** selects a particular installation. An empty path follows Kilo's current verified copy, including a custom version installed through Kilo, then the system installation when there is no managed copy. An explicit path must be absolute and available; an invalid or removed executable stops the launch rather than silently choosing another version. Use **Automatic detection** to follow the managed copy again. Installing a new Kilo copy does not erase an explicit path.

On Windows, Kilo recognizes Meta's official `muse.cmd` launcher and resolves the active versioned `.exe` from that same installation. This keeps quotes and multiline prompts intact. For other custom Windows wrappers, select the underlying Muse `.exe` directly.

The terminal wrapper, the Muse harness and T3 use this same selection. Save path changes before opening. Close and prepare the private T3 workspace again after changing its resolved Muse executable. Kilo launches Muse with automatic self-update disabled so a running CLI cannot replace the verified installation; update through Kilo or your chosen external installation instead.

## Connection and models

**Kilo Proxy · shared models** uses the models already saved in Kilo **Models**, their exact gateway IDs and your Kilo connection, in a separate Muse profile. Connect Kilo or ChatGPT and save at least one compatible model first. The launcher does not select models for you or change your ordinary Muse configuration, Meta account or chats. Changes to the connection, models or selected executable require preparing the Kilo profile again; opening through Kilo performs that preparation. Available tools and reasoning depend on the selected model, gateway and installed Muse CLI.

The Kilo connection pins Muse's file credential backend for its private profile and version checks. Its private `auth.json` contains only a scoped local Kilo proxy credential; it does not import your Meta login. **Own Meta account** retains Muse's native credential-store selection.

**Own Meta account** keeps Muse's existing login, official catalog, configuration and chats. It does not require Kilo shared models or gateway credentials. Your Kilo login does not sign in to Meta. The two connections remain separate; changing the selection does not convert native chats into Kilo chats.

Both connections use the executable selected under **Settings → Application and CLI installations**. Installation detection does not prove that a particular CLI version supports Kilo configuration. The preparation step checks that exact executable and rejects incompatible versions before opening.

## Terminal

Install or update the wrappers in **Settings → Terminal commands**. Open a new terminal and run:

```sh
cd /path/to/your/project
kilo-muse
```

The command uses the current directory, forwards arguments and preserves the CLI's exit code. It follows the connection chosen in the Muse harness. Keep Kilo Proxy open for **Kilo Proxy · shared models**. Only **Own Meta account** also works with the Kilo app closed; it reads the saved executable selection locally.

For **Own Meta account**, sign in through Muse's own flow:

```sh
kilo-muse login
```

In **Own Meta account** mode, the login prompt on first opening is expected: this connection requires Meta authentication, and signing in to Kilo Proxy does not authenticate Meta. In an interactive Muse terminal, use `/login`, then `/models` to choose from its catalog. The available models and permissions come from your Muse account. You can also pass an exact model ID that your account supports:

```sh
kilo-muse --model <official-model-id> --reasoning-effort high
kilo-muse resume --last
```

Use `kilo-muse --help` and command-specific `--help` for the options supported by your installed version. Kilo does not replace the selected model with a similarly named gateway model, write Meta credentials or alter your normal Muse session storage. Muse's tool approvals and sandbox defaults remain active.

**Open Muse Code** in Harnesses starts a new terminal in the general project folder or the Muse-specific override. Its gear shows the selected connection and model source. The Kilo connection prepares its separate profile; the Meta connection uses only an executable preflight and its native login/catalog. See [terminal commands](terminal-commands.md) for wrapper installation and manual shell setup.

## T3 Code

In **Harnesses → T3 Code → gear → Accounts and models** in Kilo Proxy, enable the optional **Muse Code** provider and choose its **Muse connection**. Newly enabling Muse selects **Kilo Proxy** with **Meta models**. Under **Muse Kilo models**, choose the models published by Meta in the connected Kilo catalog (including Muse and Llama), or **All shared models** to use the complete saved selection. Both use Kilo as the backend and preserve exact gateway model IDs. Existing Kilo Muse accounts keep **All shared models** until you change this policy. If the connected catalog has no Meta models, refresh it or choose **All shared models** before preparing. Existing saved native Muse choices stay native until you explicitly change them. Close **T3 Code · Kilo** and prepare or open it again after changing the connection. The installed T3 package must include a compatible native Muse driver and SDK. A missing or incompatible driver produces a preparation error.

To use only native accounts, disable **Kilo Proxy · Codex** and **Kilo Proxy · Claude**, keep OpenCode disabled or choose **Own OpenCode account**, and keep Muse disabled or choose **Own Meta account**. This mode does not require a Kilo gateway connection or shared models and does not create Kilo CLI profiles or proxy secrets. Enabling any Kilo connection, including OpenCode’s shared models or Muse's **Kilo Proxy** choice, requires its Kilo connection and the model source configured for that account.

T3 receives the same selected Muse executable. **Kilo Proxy · Muse Code** has a separate account ID/profile and receives the saved selection filtered by its model policy. **Muse Code · Meta** retains its native account ID and obtains its own catalog through Muse's session protocol. Enabling the provider alone does not convert an existing native conversation. The installed T3 package must support the selected Muse connection and isolation contract; otherwise preparation is blocked. Model availability and reasoning controls depend on the installed Muse/T3 versions and the selected connection. Only the native Meta choice uses your Meta account. The [official Muse SDK](https://github.com/meta-models/muse-code-sdk) uses a `muse serve` process; Meta documents the SDK and CLI versions as paired releases.

When switching providers in a compatible T3 Orchestrator V2 workspace, T3's existing bounded handoff applies. This is separate from Muse's native terminal chats. See [T3 Code](t3-code.md) for provider settings and private-workspace behavior.

## Troubleshooting

- **Muse was not found:** install it or select its executable under **CLI installations**, save and refresh detection.
- **Selected executable unavailable:** restore that exact installation or return to automatic detection. Kilo will not substitute another installed version.
- **Executable changed:** refresh installation detection before opening again; prepare the closed T3 workspace again if its resolved executable changed.
- **Kilo models unavailable:** connect Kilo or ChatGPT, save the intended models in **Models**, check the selected executable and refresh the Muse profile. An unsupported CLI is not replaced silently.
- **Meta login or models unavailable:** choose **Own Meta account**, use Muse's own login and inspect its account access. Saving Kilo gateway credentials does not sign in to Meta.
- **T3 Muse driver unavailable:** use a T3 build that includes a compatible native Muse driver. Installing the CLI alone cannot add that driver to T3.
