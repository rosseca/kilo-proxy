# Terminal commands

`kilo-codex`, `kilo-claude`, `kilo-opencode` and `kilo-omp` launch your installed Codex CLI, Claude Code, OpenCode or Oh My Pi through Kilo Proxy in the terminal and project folder you are already using. These commands are available on macOS and Linux.

## Install once

1. Open Kilo Proxy, save your account and organization under **Settings**, and choose your shared library in **Models**. Install the CLI you want to use separately: Codex CLI, Claude Code, OpenCode or Oh My Pi (`omp`).
2. Open **Settings → Terminal commands** and click **Install terminal commands**. The Codex CLI, Claude Code, OpenCode and Oh My Pi cards also link here from **Options → Terminal commands in Settings**.
3. Open a new terminal so it reads the updated PATH. Change to your project folder and run the command for your installed CLI.

The installer writes all four commands, `kilo-codex`, `kilo-claude`, `kilo-opencode` and `kilo-omp`, into `~/.local/bin` for your user. You can install the commands even if you only use one of those agents. It requires zsh, bash or fish as your login shell and manages a marked PATH block in that shell's startup files. Settings shows the install location and startup files. Existing shell settings outside the marked block are preserved.

**Update terminal commands** reinstalls the managed commands after an application update or move. **Check installation** refreshes their status. Existing unrelated files with the same names are not replaced.

If you installed terminal commands before `kilo-opencode` was available, click **Install terminal commands** again to add it; complete installations show **Update terminal commands** instead. Installing a newer Kilo Proxy build alone does not create the new wrapper.

## Use your current terminal

```sh
cd /path/to/your/project
kilo-codex
```

Or run Claude Code:

```sh
kilo-claude
```

Or run OpenCode:

```sh
kilo-opencode
```

Or run Oh My Pi:

```sh
kilo-omp
```

Keep Kilo Proxy open while working; its window can be closed to the system tray or menu bar. If the saved proxy connection is stopped, the command starts it before launching the agent. If the app has been quit, reopen it and run the command again.

Each invocation prepares the latest **saved** shared models, names, default and supported reasoning preferences. Finish any pending model save in the app before launching. Changes made after an agent starts apply on its next launch.

The commands forward the arguments you supply to the underlying CLI. For example, select a previous conversation in the Kilo profile:

```sh
kilo-codex resume
kilo-claude --resume
kilo-opencode --continue
kilo-omp --resume
```

These are the native resume commands documented by [OpenAI](https://learn.chatgpt.com/docs/developer-commands?surface=cli#codex-resume), [Claude Code](https://code.claude.com/docs/en/sessions#resume-a-session), [OpenCode](https://opencode.ai/docs/cli/) and [Oh My Pi v18.2.9](https://github.com/can1357/oh-my-pi/blob/v18.2.9/packages/coding-agent/src/commands/launch-help.ts). OpenCode also accepts `--session <id>` for a specific session. OMP's bare `--resume` opens its session picker. Argument behavior depends on your installed CLI version.

OpenCode subcommands and model overrides work too:

```sh
kilo-opencode models kilo-local
kilo-opencode run "Summarize this project"
kilo-opencode --model kilo-local/vendor/model-id
```

No additional terminal window opens. The CLI uses the current working directory and the terminal's input/output; when it exits, you return to your shell.

## Profiles and authentication

`kilo-codex` prepares `~/.codex-kilo-cli`; `kilo-claude` prepares `~/.claude-kilo`; `kilo-omp` prepares `~/.omp-kilo`. These are the same isolated profiles used by their **Open** buttons in Agents. Their saved sessions belong to those profiles. Normal `codex`, `claude` and `omp` keep their existing configuration and authentication. See [Oh My Pi setup](oh-my-pi.md) for its model picker, reasoning and image MCP support.

`kilo-opencode` refreshes `~/.opencode-kilo/opencode.json`, the configuration used by **Open OpenCode**, with the shared models, display names, default and context limits. It also updates the managed `kilo_images` MCP entry from the image-generation setting while preserving unrelated settings and MCP servers. The command sets `OPENCODE_CONFIG` for its child process and clears inherited `OPENCODE_CONFIG_CONTENT`. It does not edit your ordinary OpenCode configuration or authentication; global/project settings still merge, and OpenCode's usual session storage is shared. Those settings can affect the effective configuration. See [OpenCode setup](opencode-and-zed.md).

The installed commands contain no API keys. They contact the running local Kilo Proxy app, which prepares the profile and supplies the local connection credential to the child process. Your personal Kilo key is not placed in the shell command or shell startup files. Profile compatibility and model protocol requirements are described in [client setup](clients.md).

## Troubleshooting

- **Command not found:** Open a new terminal after installation. If it still cannot find the commands, check their paths in Settings and use the full path or add the install directory to PATH.
- **Kilo Proxy is unavailable:** Reopen the app with the same configuration directory used when installing the commands. Save a working account and organization in Settings, then try again.
- **Agent not found:** Install the matching client: Codex CLI, Claude Code, OpenCode or Oh My Pi (`omp`). The terminal commands launch existing clients; they do not install them.
- **Commands stopped working after moving the app:** Open Kilo Proxy from its new location and use **Update terminal commands**.
- **Model selection is empty or stale:** Save models in the common library and launch again. An already running agent does not automatically reload it.

Windows users can continue to launch Codex CLI, Claude Code, OpenCode and Oh My Pi from their **Open** buttons in Agents; these terminal commands are available on macOS and Linux.

## Verification

Automated tests cover installation, preserved shell startup files, repeated updates, argument quoting, long prompts, current directory, stdin/stdout, exit codes, saved model changes and local authentication. They use temporary homes, fake clients and a local app instance, without Kilo inference.

Before packaging, GitHub Actions also runs the production executable's terminal entrypoint on both macOS and Linux architectures with no graphical session. To repeat that check locally:

```sh
KILO_TEST_TERMINAL_BINARY=/absolute/path/to/kilo-proxy go test . -run '^TestTerminalAgentRunsInCurrentTerminal$' -count=1
```

For an optional smoke check with an installed OpenCode CLI, set both executable paths. This lists two synthetic shared models through the actual wrapper without model inference, using temporary configuration and data directories:

```sh
KILO_TEST_TERMINAL_BINARY=/absolute/path/to/kilo-proxy \
KILO_TEST_OPENCODE_BINARY=/absolute/path/to/opencode \
go test . -run '^TestTerminalOpenCodeInstalledCLILoadsSharedModels$' -count=1
```
