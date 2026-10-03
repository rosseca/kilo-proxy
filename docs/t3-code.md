# T3 Code

Kilo Proxy opens **T3 Code · Kilo**, a separate T3 Code workspace with four agent options:

| Agent | Models and connection | Login |
| --- | --- | --- |
| Codex · Normal | Codex's normal model catalog | Your existing Codex CLI login |
| Codex · Kilo Proxy | Kilo Proxy's shared models, through the local proxy | Private Kilo CLI profile |
| Claude · Normal | Claude's normal model catalog | Your existing Claude Code login |
| Claude · Kilo Proxy | Compatible shared models, through the local proxy | Private Kilo CLI profile |

This integration uses **Codex CLI and Claude Code**, rather than either Desktop application. A Desktop app login alone does not establish a CLI login.

## Requirements

- Install [T3 Code](https://github.com/pingdotgg/t3code/releases/tag/v0.0.45) **0.0.45**, the currently validated release. Other releases are rejected until their settings contract is verified.
- On Linux, use an extracted AppImage installation so the executable and its package metadata are accessible to the launcher.
- Install Codex CLI and Claude Code so Kilo Proxy can find their executables.
- Sign in to the CLIs normally to use the normal agent options.
- Connect Kilo Proxy to your provider and save at least one model in **Models**.

The preparation step checks the installed T3 Code contract before writing its private settings. An unsupported or incomplete installation produces an error instead of modifying your usual T3 Code workspace.

## Open the workspace

1. In Kilo Proxy, open **Agents → T3 Code · Kilo**.
2. Select **Open T3 Code · Kilo**. Kilo Proxy prepares the four agents with the saved shared models and starts the proxy first.
3. Create a new chat in T3 Code and select an agent.

**Integration settings → Prepare without opening** performs the same preparation without starting T3 Code. The local web interface also offers a **T3 Code · Kilo** integration tab.

Your regular T3 Code workspace can remain open. Its preferences, projects and chat history are separate and are not imported. The normal agents in the Kilo workspace refer to the usual CLI homes so they use the existing CLI login; Kilo Proxy does not copy login data or sign you out.

If the environment that starts Kilo Proxy sets an absolute `CODEX_HOME` or `CLAUDE_CONFIG_DIR`, the normal agent uses that home instead of `~/.codex` or `~/.claude`. These paths are recorded per agent; they are not applied to the Kilo agents. Changing them requires preparing again with the private T3 workspace closed.

Close **T3 Code · Kilo** before changing shared models or the proxy connection, then open it again from Kilo Proxy. Only the private Kilo workspace must be closed. This prevents changing an agent configuration while a chat is using it.

## Models, reasoning and chats

Manage the Kilo agents' models in Kilo Proxy's shared **Models** library. The normal agents keep their own model catalogs.

In **Claude · Kilo**, choose a prepared model by its exact gateway ID. T3 Code 0.0.45 also lists its built-in Claude models alongside the prepared models. Those extra entries are not mapped to your shared library and may not be available through your provider.

Codex · Kilo exposes the supported reasoning levels for each model. Claude · Kilo exposes only the levels supported by its Claude Code driver and the model; its options can be narrower than Codex's. Model listing does not establish support for generation, tool use or every reasoning level. The selected provider must support the protocol and features used by the agent.

A chat keeps its agent and native session. To switch between a normal agent and a Kilo agent, create a new chat. Model changes within a compatible agent follow T3 Code's own session and resume behavior.

## Troubleshooting

- **T3 Code was not found:** install the Desktop application, then use **Options → Refresh detection**. Installing a CLI or the source repository alone is not the Desktop installation.
- **Codex or Claude Code was not found:** install the missing CLI and refresh detection. On Windows, the Microsoft Store Codex app and Claude Desktop are separate from the CLI executables used here.
- **Normal agent needs login:** sign in using that CLI normally. Avoid entering provider credentials into the Kilo agent configuration.
- **Close T3 Code · Kilo first:** quit the private Kilo T3 window, then prepare or open again. Your regular T3 Code window can remain open.
- **Shared models changed while preparing:** review the refreshed model list and open again. Preparation uses a saved snapshot so a concurrent edit cannot silently launch a different model selection.
- **A custom model is listed but requests fail:** inspect the proxy request to identify the provider response. The Codex route requires Responses support; Claude Code requires Anthropic Messages support. Tool and reasoning support depend on the model and gateway.
- **Proxy credentials changed:** close the private T3 workspace and reopen it from Kilo Proxy so the prepared agents receive the current local connection.

The separate workspace stores only the local proxy connection for the Kilo agents. Upstream Kilo and ChatGPT account credentials remain managed by Kilo Proxy. Do not put the Kilo connection in T3 Code's global environment: it would also affect the normal agent options.
