# Headless console and server mode

`kilo-proxy-headless` runs Kilo Proxy on macOS and Linux without a desktop, browser panel, system tray, or OS credential store. It exposes the same local inference endpoints and prepares the same `kilo-codex`, `kilo-claude`, `kilo-opencode`, and `kilo-omp` terminal agents.

The default configuration directory is the operating system's user configuration directory followed by `kilo-proxy-headless`. It is separate from the desktop application's `kilo-proxy` profile. Use `--config-dir /absolute/path` before the command to select another headless profile. A profile has one controller at a time: commands contact its running controller, or open the profile temporarily when no controller is running. They do not silently share the desktop application's credentials or modify normal Codex/Claude profiles.

## Configure a connection

Install the headless archive matching your operating system and architecture, then place its executable on your PATH. Install the underlying agents separately.

For Kilo device login:

```sh
kilo-proxy-headless login kilo
```

The command prints a verification URL and code. Open the URL in any browser, including on another machine, and approve the sign-in. If the account has multiple organizations, run `login kilo --org ORGANIZATION_ID` to select one. A completed login saves the connection to this headless profile. Ctrl+C or SIGTERM cancels the pending device flow.

After a successful login or `configure`, and when starting `serve`, the CLI checks whether the four `kilo-*` commands are already installed for this executable and profile. If setup is needed, it prints the command to run on this machine, with the current executable and any custom `--config-dir` quoted correctly. The suggestion does not install anything or change shell settings. Running `commands` also shows setup guidance.

To supply an existing Kilo key, read it from a private file:

```sh
chmod 600 /path/to/kilo-key
kilo-proxy-headless configure --org ORGANIZATION_ID --key-file /path/to/kilo-key --port 8878
```

Or pipe it from your secret manager:

```sh
your-secret-manager-read-command | kilo-proxy-headless configure --org ORGANIZATION_ID --key-stdin --port 8878
```

Keys are never accepted as command-line arguments or printed by setup commands. The key file must be a regular file with private permissions; symlinks are rejected. One trailing newline from a pipeline is accepted. Keys are limited to 8,192 bytes and cannot contain whitespace or control characters. Omitting both key input flags preserves the existing Kilo key, so changing only the organization or port does not require entering it again.

To connect a ChatGPT subscription instead, or alongside Kilo:

```sh
kilo-proxy-headless login chatgpt
```

This uses the existing ChatGPT device OAuth flow. It does not import a normal Codex login, saved browser session, or another headless profile's credentials. Access-token refresh is automatic. `chatgpt/` model IDs distinguish subscription requests from Kilo credit requests. Sign-in still requires browser approval; running as a service does not automate that approval.

Authentication changes require the inference proxy to be stopped. With a controller running, use `proxy stop`, make the change, then use `proxy start`. Use `logout kilo` or `logout chatgpt` to remove only that provider's saved sign-in from this profile.

## Choose models, reasoning, and context

```sh
kilo-proxy-headless models catalog --refresh
kilo-proxy-headless models add EXACT_MODEL_ID --reasoning high --context low
kilo-proxy-headless models default EXACT_MODEL_ID
kilo-proxy-headless models list
```

`catalog` lists exact IDs and available context, output, tools, and reasoning metadata. Add `--json` for the complete catalog. Catalog metadata is cached for one hour; a missing or old cache refreshes when browsing or adding models. A failed refresh preserves the previous cache. Partial results remain immediately eligible for another refresh.

`models add` adds a model or updates its supplied preferences without changing other existing preferences. The first added model becomes the default. Available options are `--name`, `--reasoning`, `--context`, and `--context-tokens`. Reasoning levels must match the published model capabilities. `auto` uses the normal catalog/default behavior; `none` is an explicit level where the model supports it. Context is `recommended`, `low`, `maximum`, or `custom`; Custom requires `--context-tokens N`. Maximum requires a known catalog capacity. Context policies are working budgets capped by that capacity; they do not increase provider limits.

```sh
kilo-proxy-headless models add EXACT_MODEL_ID --name 'Daily model' --context custom --context-tokens 128000
kilo-proxy-headless models remove EXACT_MODEL_ID
kilo-proxy-headless models export models.json
kilo-proxy-headless models import models.json
```

Export/import uses the shared model-library JSON, without credentials or inferred provider capabilities. Use `-` for stdout/stdin. Import does not contact the model provider, making it suitable for automated deployment or models with explicit advanced metadata. JSON must pass the same strict schema and capability-override validation as the desktop library and fit within 128 KiB. Updates use the current saved revision and refuse conflicting writes. A damaged library is not silently replaced: inspect/export its recovered state, then use `models import --recover FILE` to explicitly recover it.

Example library:

```json
{
  "schemaVersion": 1,
  "defaultModel": "vendor/exact-model-id",
  "models": [
    {
      "id": "vendor/exact-model-id",
      "displayName": "Daily model",
      "contextPreset": "low",
      "reasoningEffort": "high"
    }
  ]
}
```

Importing a library does not prove model availability or account permissions. Existing saved model packs remain effective for terminal agents assigned to them; headless model commands edit the shared library, not pack assignments.

## Run and use terminal agents

```sh
kilo-proxy-headless serve
```

`serve` stays in the foreground until Ctrl+C, SIGTERM, or `kilo-proxy-headless stop`. Open another terminal to query `status`, install wrappers, or launch an agent. The inference listener stays on loopback. If the desktop proxy is already using 8877, choose another port such as 8878 in the headless profile.

Use `serve --setup` to keep a controller running before an account has been configured, then run setup commands in another terminal. `proxy stop` stops only inference while keeping this controller available; `proxy start` starts inference again. `stop` shuts down the controller, drains active HTTP requests for up to 15 seconds, and waits for cleanup before releasing the profile. The stop command can wait up to 75 seconds for that cleanup. Repeated `serve` for a live profile is refused rather than starting a second process on its files.

```sh
kilo-proxy-headless commands install
cd /path/to/project
kilo-codex
# Or: kilo-claude, kilo-opencode, kilo-omp
```

The explicit `commands install` command writes wrappers into `~/.local/bin` and updates the managed PATH block for the current user's Zsh, Bash, or Fish login shell. It preserves unrelated shell settings and refuses conflicting wrapper files. The output lists the startup files it updated and a quoted `source` command to activate them in the matching shell; opening a new terminal also activates the PATH. Use `commands install --json` when a script needs installation metadata.

Run `commands status` to inspect installation, or `commands manual` to print Zsh/Bash functions without editing files. An unsupported shell receives manual setup guidance: review the printed functions and add them to a Bash/Zsh startup file, or use a supported login shell. Install commands from the same headless profile you intend to serve. These wrapper names are shared with desktop installations: installing them selects which executable and Kilo profile they contact. Rerun the installer after moving the executable.

For a second profile, the suggestion retains its exact directory, for example:

```sh
'/opt/kilo/bin/kilo-proxy-headless' --config-dir '/srv/my account/kilo-profile' commands install
```

Each wrapper uses the current project directory, forwards arguments and stdin/stdout, and returns the underlying agent's exit code. It prepares the latest saved shared models or assigned pack, then starts the inference proxy if it is stopped. The controller must remain running for the conversation. Normal `codex`, `claude`, and `omp` commands retain their own profiles and authentication. See [terminal command behavior and isolation](terminal-commands.md).

Headless-generated agent profiles live in this profile's private `profiles/codex`, `profiles/claude`, `profiles/opencode`, and `profiles/omp` directories. Different headless controllers do not overwrite each other's generated configuration or the desktop terminal agent profiles. Install an underlying agent CLI separately if its command is unavailable on the server; the wrappers do not install agent software.

## Credentials, remote access, and services

The headless configuration directory is private (0700). Its file credential vault uses private files (0600) and stores credential-vault values in plaintext, unlike the desktop OS keyring. ChatGPT OAuth credentials still use the existing encrypted credential format, but the encryption key is in this private file vault. Someone able to read the entire headless profile can obtain its credentials. Protect it with your server account permissions, disk encryption, and appropriately restricted backups. Do not commit or share the profile directory.

`status`, catalog, and library commands omit upstream credentials and the local proxy key. `connection` prints the local base URL without its key. For another API client, explicitly export the local connection credential to a private file:

```sh
umask 077
kilo-proxy-headless connection --json --show-key > local-connection.json
```

Keep this file private: its key authorizes local inference. This command never exports the upstream Kilo key or ChatGPT tokens. Generated agent profiles contain the local connection credential where their clients require it and are kept private. Request-detail capture remains off by default. Serve runs as the invoking account, not as a system-wide account with implicit access to another user's profile.

Keep the inference and control listeners on loopback. For remote development, run the terminal agent over SSH on the server, or use an SSH tunnel to an explicitly configured local client. Neither listener should be published directly to the Internet. A service should run under the same account and use the same absolute headless configuration directory as setup commands. Complete device login interactively before enabling unattended startup, and install underlying agents on the host where their wrappers run.

For per-user service management:

```sh
kilo-proxy-headless service install
kilo-proxy-headless service start
kilo-proxy-headless service status
# Later: service stop, or service uninstall
```

On Linux, install writes a profile-specific unit into the user systemd configuration, reloads the user manager, and enables the unit. Starting it is a separate command. An SSH/server account needs an available systemd user manager; continuing after logout may require an administrator to enable user lingering. Kilo Proxy does not enable lingering or use sudo.

On macOS, install generates a profile-specific launchd plist inside the headless profile. Start loads it into the invoking account's launchd user domain, including from SSH without a graphical session; stop unloads it. This does not install a system daemon or promise automatic loading after a reboot. Uninstall removes only the service metadata; it preserves saved credentials, models, and sessions.

Both service definitions capture the current HOME, PATH, XDG_CONFIG_HOME, absolute executable path, and headless configuration path without embedding credentials. SHELL and ZDOTDIR are preserved when nonempty, so command installation still targets your shell's configured startup directory. Install with the environment your agents need, and reinstall after moving the executable. File credentials permit unattended startup without unlocking a desktop keyring; they do not remove provider expiration, revocation, quota, or model-access rules. Use `service --help` for command usage.

For macOS startup before any login, an administrator can configure a LaunchDaemon under `/Library/LaunchDaemons` or an existing server supervisor. Run its `ProgramArguments` as `kilo-proxy-headless --config-dir /absolute/private/profile serve` and set `UserName` to the account that owns that profile, with that account's HOME, PATH, and SHELL. Keep the executable at a stable absolute path and credentials in the private profile; no credentials belong in a plist. Use a separate label and system service definition rather than copying the generated user-service ownership record. This administrator-managed setup is outside `service install` and must be validated on that host.
