# T3 Code

Kilo Proxy opens **T3 Code · Kilo**, a separate T3 Code workspace with four agent options:

| Agent | Models and connection | Login |
| --- | --- | --- |
| Codex · Normal | Codex's normal model catalog | Your existing Codex CLI login |
| Kilo Proxy · Codex | Kilo Proxy's shared models, through the local proxy | Private Kilo CLI profile |
| Claude · Normal | Claude's normal model catalog | Your existing Claude Code login |
| Kilo Proxy · Claude | Compatible shared models, through the local proxy | Private Kilo CLI profile |

The Kilo agents have a green **KP** badge on T3's provider rail and composer icons. Normal agents keep their usual icons; small model-row logos retain T3's design, with the **Kilo Proxy** label identifying the Kilo connection.

This integration uses **Codex CLI and Claude Code**, rather than either Desktop application. A Desktop app login alone does not establish a CLI login.

## Requirements

- Install [T3 Code Desktop](https://github.com/pingdotgg/t3code/releases), using stable, alpha, beta, nightly or another release channel. Kilo Proxy checks the installed package identity and integration contract; versions and commits are informational, not an allowlist.
- On Linux, use an extracted AppImage installation so the executable and its package metadata are accessible to the launcher.
- Install Codex CLI and Claude Code so Kilo Proxy can find their executables.
- Sign in to the CLIs normally to use the normal agent options.
- Connect Kilo Proxy to your provider and save 1–32 shared models in **Models**. T3 accepts at most 32 custom models per agent; preparation rejects larger libraries so the initial model cannot silently disappear.

The preparation step checks the installed T3 Code contract before writing its private settings. An incompatible or incomplete installation produces an explicit error instead of modifying your usual T3 Code workspace.

The launcher recognizes channel names in the system and user application directories, including alpha, beta and nightly. Known nightly installations take priority, followed by stable and other installed channels. Linux also checks the installed `t3code` / `t3-code` executable and extracted app directories. Integration settings show the detected version.

After updating T3 or installing Kilo Proxy 0.57.0, close **T3 Code · Kilo**, refresh detection and prepare or open it again. Your private chats are retained. Preparation records the installed desktop and backend source digests, so an in-place rebuild with the same version also requires preparation again. These structural checks catch incompatible settings or isolation contracts; they cannot guarantee compatibility with every future upstream change.

T3 builds using Orchestrator V2 have their own database migration. On first opening the private Kilo workspace, T3 makes a one-time copy of its V1 database into a separate V2 database. The V1 database is retained, but new chats and changes do not synchronize between them. This applies only to Kilo's private workspace; the launcher does not open your regular T3 data directory. Use web/mobile clients compatible with the installed T3 server protocol.

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

Preparation configures the private Desktop model picker for **Kilo Proxy · Claude**: it hides the built-in Claude entries that are not exact IDs in your shared library and orders your prepared models with the shared default first. Choose the model by its saved display name; the request keeps its exact gateway ID, such as `anthropic/claude-opus-5.5`. Normal agents keep their own catalogs and preferences.

Close **T3 Code · Kilo** and reopen it from Kilo Proxy to apply this picker policy to an older workspace. A chat, project or draft using a hidden built-in model falls back to the shared default shown in the composer; review or change the selected model before continuing. Preparation also includes Claude entries from T3’s private cached manifest. T3 can refresh that manifest independently of its app version; if a new built-in entry appears, close and reopen the Kilo workspace to prepare again. This policy belongs to the Desktop client preferences; independently connected web/mobile clients keep their own picker preferences.

Kilo Proxy · Codex exposes the supported reasoning levels for each model. In T3 builds using Orchestrator V1, Kilo Proxy · Claude exposes only the levels supported by its Claude Code driver and the model; its options can be narrower than Codex's. Model listing does not establish support for generation, tool use or every reasoning level. The selected provider must support the protocol and features used by the agent.

In T3 builds using Orchestrator V1, a chat keeps its agent and native session. To switch between a normal agent and a Kilo agent, create a new chat. Model changes within a compatible agent follow T3 Code's own session and resume behavior.

T3 builds using the supported Orchestrator V2 contract can switch agents between turns in the same chat. Each normal or Kilo agent keeps its own CLI home and connection. T3 hands off a bounded summary when switching providers; previous reasoning, tool results and attachments are not transferred. Use a new chat when you need to keep work completely separate.

The supported Orchestrator V2 contract resolves Claude effort through its built-in catalog rather than custom gateway IDs. For **Kilo Proxy · Claude**, choose compatible reasoning defaults in Kilo Proxy’s **Models**, then close and reopen the private T3 workspace. Kilo prepares per-model Claude Code settings and hides the ineffective T3 effort selector. These defaults require Claude Code **2.1.251 or newer**; preparation rejects a configured compatible effort with an older CLI. Automatic and unsupported Claude levels do not receive a native effort override. Saved Claude reasoning defaults also require provider-qualified gateway IDs, such as `anthropic/claude-opus-4-6`: T3 can apply its own builtin effort for unqualified IDs, so preparation rejects that combination. IDs of the same Claude family/version cannot have conflicting prepared efforts, because Claude Code keys its defaults by family/version. Codex’s T3 reasoning selector remains available.

## Troubleshooting

- **T3 Code was not found:** install the Desktop application, then use **Options → Refresh detection**. Installing a CLI or the source repository alone is not the Desktop installation.
- **Incompatible T3 settings contract:** update T3 or Kilo Proxy and prepare again. A new channel or version is not rejected merely for its version number; an incompatible private-workspace, provider, secret-store or model-picker structure is rejected before changing private settings.
- **Codex or Claude Code was not found:** install the missing CLI and refresh detection. On Windows, the Microsoft Store Codex app and Claude Desktop are separate from the CLI executables used here.
- **Normal agent needs login:** sign in using that CLI normally. Avoid entering provider credentials into the Kilo agent configuration.
- **Close T3 Code · Kilo first:** quit the private Kilo T3 window, then prepare or open again. Your regular T3 Code window can remain open.
- **Shared models changed while preparing:** review the refreshed model list and open again. Preparation uses a saved snapshot so a concurrent edit cannot silently launch a different model selection.
- **A custom model is listed but requests fail:** inspect the proxy request to identify the provider response. The Codex route requires Responses support; Claude Code requires Anthropic Messages support. Tool and reasoning support depend on the model and gateway.
- **Proxy credentials changed:** close the private T3 workspace and reopen it from Kilo Proxy so the prepared agents receive the current local connection.

The separate workspace stores only the local proxy connection for the Kilo agents. Upstream Kilo and ChatGPT account credentials remain managed by Kilo Proxy. Do not put the Kilo connection in T3 Code's global environment: it would also affect the normal agent options.
