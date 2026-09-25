# Oh My Pi

[Oh My Pi](https://omp.sh), also called **OMP**, is a terminal coding agent. Kilo Proxy can prepare its models and credentials, start the proxy, and open OMP in a terminal in your chosen project. It is a separate integration from Pi and from OpenCode.

## Open an agent

1. Install OMP using its [official installation instructions](https://github.com/can1357/oh-my-pi#install). Kilo Proxy detects `omp` on the executable search path and common installation locations. It does not install or update OMP itself.
2. In Kilo Proxy, choose your models in **Models**, including their display names, default and supported reasoning preferences.
3. Go to **Agents → Oh My Pi**, choose a project folder, and click **Open Oh My Pi**. Kilo Proxy prepares its dedicated profile and starts the saved proxy connection before opening the terminal.

The terminal opens in Terminal on macOS, a console on Windows, or an installed desktop terminal on Linux. Use **Options → Refresh detection** after installing OMP if the card still reports it missing.

Inside OMP, `/model` opens its model picker and `Ctrl+P` cycles the enabled models. Both use the shared library exported by Kilo Proxy. Your ordinary `omp` sessions retain their normal profile; the generated Kilo profile is used by the Kilo launcher, `kilo-omp` or an exported launch command.

## Use your current terminal

On macOS, Linux and Windows, **Settings → Terminal commands** installs `kilo-omp` together with `kilo-codex`, `kilo-claude` and `kilo-opencode`. Use **Install terminal commands** on macOS/Linux or **Install PowerShell functions** on Windows; manual copy blocks are also available for Zsh/Bash and PowerShell. If you installed an older set of commands, use **Install terminal commands** or **Update terminal commands** once to add any missing wrappers. Install Oh My Pi separately first; the wrapper needs an existing `omp` executable.

Open a new terminal, change to your project, and run:

```sh
cd /path/to/your/project
kilo-omp
```

The command prepares the latest saved shared models in the same `~/.omp-kilo` profile used by **Open Oh My Pi**. It uses your current terminal and project directory, forwards arguments to OMP, and starts the saved proxy connection if it is stopped. Keep the Kilo Proxy app running, including in the tray; reopen the app first if it has been quit.

For example, `kilo-omp --resume` opens OMP's session picker for that isolated profile. See [terminal command installation, PATH setup and troubleshooting](terminal-commands.md).

## Profile and model settings

The dedicated profile is `~/.omp-kilo` on macOS and Linux, or `%USERPROFILE%\.omp-kilo` on Windows:

| File | Purpose |
| --- | --- |
| `models.yml` | The `kilo-local` provider, exact model IDs, display names, token limits, supported input types and reasoning settings |
| `config.yml` | The default model and enabled model list; the generated profile skips OMP's initial setup wizard |
| `mcp.json` | The Kilo image MCP connection when image generation is enabled |
| `kilo-models.json` | Kilo Proxy's saved OMP model selection, without credentials |

Preparation preserves unrelated settings and makes `.bak` copies of changed existing files. Invalid configuration or unsafe file destinations stop preparation instead of overwriting them.

YAML anchors, aliases and merge keys are also rejected before any profile files are written. Expand these constructs into explicit values in `models.yml` or `config.yml` before preparing again. This prevents a managed update from breaking an alias or silently removing inherited settings.

The provider uses **OpenAI Responses** at the local proxy's `/v1` URL, with WebSockets disabled. OMP sends the full conversation on subsequent turns; the launcher disables server-side response storage/chaining. Large image handling therefore follows **Settings → Large images**, including local compression, Cloudflare quick tunnel, Litterbox, Tailscale Funnel or experimental Kilo uploads. These are proxy-wide settings; preparing OMP does not enable or reconfigure OMP's own **Serve Images as URLs** backends. Existing remote image URLs pass through unchanged. See [image transport setup and lifetime](image-uploads.md).

The generated profile contains the **local proxy key**, not your upstream Kilo credential. These files are private configuration, not material to commit or share. Kilo Proxy retains your upstream key and applies the selected organization's header when forwarding requests.

The launcher sets `PI_CODING_AGENT_DIR` to the dedicated directory and clears inherited `OMP_PROFILE` and `PI_PROFILE` selections. Those profile variables otherwise take precedence over a custom directory. It also sets `PI_OPENAI_STATEFUL=0`. The parent terminal environment and your usual OMP profile are not changed.

Shared library edits apply the next time you prepare or open OMP. Existing OMP sessions may need reopening to see changes. The optional browser helper has its own explicit preparation flow and model selection, as described in [client setup](clients.md).

When the library has no output limit for a model, the exporter uses a conservative maximum of 8,192 tokens, capped at that model's context window.

### Reasoning and prices

OMP supports these configurable reasoning effort names: `minimal`, `low`, `medium`, `high`, `xhigh`, and `max`. Kilo Proxy exports only declared supported efforts from the shared model settings; it does not infer reasoning levels from a model's name. Models without declared supported efforts expose no adjustable reasoning levels in the generated OMP profile; the gateway still controls any default behavior. Non-reasoning models are marked accordingly. OMP's `--thinking off` is a launch/session override, not a selectable effort added to every model.

Choosing **none** for a model's shared reasoning preference disables that model's reasoning controls in the generated OMP profile. To enable them again, choose a supported effort in Kilo Proxy, prepare the profile again, and reopen OMP. This differs from temporarily switching reasoning off inside an OMP session.

Kilo Proxy shows available input and output catalog prices in USD per million tokens. OMP requires input, output, cache-read and cache-write prices together; the Kilo catalog does not provide all four, so the exporter omits OMP's cost estimates rather than inventing cache prices. Use **Activity** in Kilo Proxy for usage and cost reported by the gateway. A missing or zero estimate inside OMP is not evidence that a request is free.

All selected models must support the gateway's Responses interface. Listing a model does not verify its inference permissions, balance or support for every reasoning level.

## Image generation

Enable image generation in Kilo Proxy and choose an image model, then prepare or open OMP again. The generated `mcp.json` connects OMP to Kilo Proxy's local **Streamable HTTP** image MCP using the local credential. No separate MCP executable is required.

OMP discovers the `generate_image` tool and can return its image results to a vision-capable model. OMP 18 normally exposes MCP tools through its on-demand `xd://` tool system, so they may not appear as top-level functions on every request; the model receives their device entries and can invoke them through OMP's `read` and `write` tools. Kilo Proxy saves the full-resolution original locally and returns a bounded preview to the conversation. See [image generation and payload limits](codex-images.md).

This integration configures the Kilo MCP. It does not configure OMP's separate built-in image generation providers or their credentials. Image requests follow the selected Kilo account and team, and may incur charges.

## Validation

The integration was checked against the released [OMP v18.2.9](https://github.com/can1357/oh-my-pi/releases/tag/v18.2.9). The CLI executable is supplied separately; Kilo Proxy does not bundle it.

An optional test runs a real OMP executable against a synthetic loopback gateway:

```sh
KILO_TEST_OMP_BINARY=/absolute/path/to/omp go test -run '^TestInstalledOMP$' -count=1 -v .
```

The test creates a temporary OMP profile and project, prepares them through Kilo Proxy's API, and checks multiple models, the default and explicit model switch, reasoning, Responses streaming, a local file tool round-trip, and image MCP discovery/generation with image replay. Model and image responses are synthetic; the test does not consume Kilo credits or use personal credentials.
