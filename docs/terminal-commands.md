# Terminal commands

`kilo-codex` and `kilo-claude` launch your installed Codex CLI or Claude Code through Kilo Proxy in the terminal and project folder you are already using. These commands are available on macOS and Linux.

## Install once

1. Open Kilo Proxy, save your account and organization under **Settings**, and choose your shared library in **Models**. Install Codex CLI or Claude Code separately.
2. Open **Settings → Terminal commands** and click **Install terminal commands**. Each CLI card also links here from **Options → Terminal commands in Settings**.
3. Open a new terminal so it reads the updated PATH. Change to your project folder and run either command.

The installer writes `kilo-codex` and `kilo-claude` into `~/.local/bin` for your user. It requires zsh, bash or fish as your login shell and manages a marked PATH block in that shell's startup files. Settings shows the install location and startup files. Existing shell settings outside the marked block are preserved.

**Update terminal commands** reinstalls the managed commands after an application update or move. **Check installation** refreshes their status. Existing unrelated files with the same names are not replaced.

## Use your current terminal

```sh
cd /path/to/your/project
kilo-codex
```

Or run Claude Code:

```sh
kilo-claude
```

Keep Kilo Proxy open while working; its window can be closed to the system tray or menu bar. If the saved proxy connection is stopped, the command starts it before launching the agent. If the app has been quit, reopen it and run the command again.

Each invocation prepares the latest **saved** shared models, names, default and supported reasoning preferences. Finish any pending model save in the app before launching. Changes made after an agent starts apply on its next launch.

The commands forward the arguments you supply to the underlying CLI. For example, select a previous conversation in the Kilo profile:

```sh
kilo-codex resume
kilo-claude --resume
```

These are the native resume commands documented by [OpenAI](https://learn.chatgpt.com/docs/developer-commands?surface=cli#codex-resume) and [Claude Code](https://code.claude.com/docs/en/sessions#resume-a-session). Argument behavior depends on your installed CLI version.

No additional terminal window opens. The CLI uses the current working directory and the terminal's input/output; when it exits, you return to your shell.

## Profiles and authentication

`kilo-codex` prepares `~/.codex-kilo-cli`; `kilo-claude` prepares `~/.claude-kilo`. These are the same isolated profiles used by their **Open** buttons in Agents. Their saved sessions belong to those profiles. Normal `codex` and `claude` keep their existing configuration and authentication.

The installed commands contain no API keys. They contact the running local Kilo Proxy app, which prepares the profile and supplies the local connection credential to the child process. Your personal Kilo key is not placed in the shell command or shell startup files. Profile compatibility and model protocol requirements are described in [client setup](clients.md).

## Troubleshooting

- **Command not found:** Open a new terminal after installation. If it still cannot find the commands, check their paths in Settings and use the full path or add the install directory to PATH.
- **Kilo Proxy is unavailable:** Reopen the app with the same configuration directory used when installing the commands. Save a working account and organization in Settings, then try again.
- **Agent not found:** Install Codex CLI or Claude Code. The terminal commands launch existing clients; they do not install them.
- **Commands stopped working after moving the app:** Open Kilo Proxy from its new location and use **Update terminal commands**.
- **Model selection is empty or stale:** Save models in the common library and launch again. An already running agent does not automatically reload it.

Windows users can continue to launch Codex CLI and Claude Code from their **Open** buttons in Agents; these terminal commands are available on macOS and Linux.

## Verification

Automated tests cover installation, preserved shell startup files, repeated updates, argument quoting, long prompts, current directory, stdin/stdout, exit codes, saved model changes and local authentication. They use temporary homes, fake clients and a local app instance, without Kilo inference.

Before packaging, GitHub Actions also runs the production executable's terminal entrypoint on both macOS and Linux architectures with no graphical session. To repeat that check locally:

```sh
KILO_TEST_TERMINAL_BINARY=/absolute/path/to/kilo-proxy go test . -run '^TestTerminalAgentRunsInCurrentTerminal$' -count=1
```
