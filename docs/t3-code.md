# T3 Code

Kilo Proxy opens **T3 Code · Kilo**, a separate T3 Code workspace. In **Harnesses → T3 Code → gear → Accounts and models**, choose which direct and Kilo connections it offers. These controls belong to the T3 Code harness detail, rather than the global Settings page:

| Agent | Models and connection | Login |
| --- | --- | --- |
| Codex | Codex's normal model catalog | Your existing Codex CLI login |
| Kilo Proxy · Codex | OpenAI/ChatGPT models from your shared selection, or all shared models | Private Kilo CLI profile |
| Claude | Claude's normal model catalog | Your existing Claude Code login |
| Kilo Proxy · Claude | Opus 5.5, Sonnet 5.5 and Fable 5.1, or all shared models | Private Kilo CLI profile |
| OpenCode (optional) | OpenCode’s own model catalog | Its own login; OpenCode 2 has a separate profile |
| Kilo Proxy · OpenCode (optional) | Models saved in Kilo’s shared library | Private Kilo profile |
| Muse Code (optional) | Official models discovered by the native Muse driver | Your Muse Code / Meta login |
| Kilo Proxy · Muse Code (optional) | Models saved in Kilo’s shared library | Separate Kilo Muse profile |

The Kilo agents have a green **KP** badge on T3's provider rail and composer icons. Direct agents keep their usual icons; small model-row logos retain T3's design, with the **Kilo Proxy** label identifying the Kilo connection.

This integration uses **Codex CLI and Claude Code**, rather than either Desktop application. A Desktop app login alone does not establish a CLI login.

The CLI executable choices in Kilo Settings are shared with T3 and the other integrations. Automatic detection uses Kilo's verified installed copy first, including a custom version installed through Kilo, then the system installation when there is no managed copy. Set an absolute executable path for Codex CLI, Claude Code or Muse Code to use a particular external installation instead. An unavailable explicit path stops preparation or opening; it does not silently switch to another version. Installing or updating a Kilo copy does not erase an explicit path. Restore automatic detection to follow Kilo's installed copy again. The Codex Desktop application path is a separate setting.

OpenCode has an independent optional executable path for each major version in its Kilo settings. T3 uses the path for its chosen OpenCode major. Changing a resolved CLI path requires preparing the closed private T3 workspace again. Direct and Kilo accounts use the same chosen executable while retaining their separate login/configuration homes.

## Requirements

- Install [T3 Code Desktop](https://github.com/pingdotgg/t3code/releases), using stable, alpha, beta, nightly or another release channel. Kilo Proxy checks the installed package identity and integration contract; versions and commits are informational, not an allowlist.
- On Linux, use an extracted AppImage installation so the executable and its package metadata are accessible to the launcher.
- Install the CLI for each enabled connection. Claude Opus 5.5 requires Claude Code 2.1.280+, Sonnet 5.5 requires 2.1.284+, and Fable 5.1 requires 2.1.257+. The default three-model selection therefore needs 2.1.284 or newer.
- Sign in to the CLIs normally to use the direct connections.
- For enabled **Kilo Proxy · Codex**, **Kilo Proxy · Claude**, **Kilo Proxy · OpenCode** or **Kilo Proxy · Muse Code** connections, connect Kilo Proxy to your provider and save shared models in **Models**. Each enabled Kilo Codex/Claude connection supports at most 32 effective models. The official Codex option filters the shared selection to exact `openai/` and `chatgpt/` IDs; the official Claude option resolves its three models from the connected catalog. Missing or ambiguous Claude IDs produce an error rather than falling back to another model. When all Kilo connections are disabled, native accounts such as Muse can prepare and open without a Kilo gateway connection or shared model library.

The preparation step checks the installed T3 Code contract before writing its private settings. An incompatible or incomplete installation produces an explicit error instead of modifying your usual T3 Code workspace.

The launcher recognizes channel names in the system and user application directories, including alpha, beta and nightly. Known nightly installations take priority, followed by stable and other installed channels. Linux also checks the installed `t3code` / `t3-code` executable and extracted app directories. The settings page shows the detected version.

After updating T3 or installing Kilo Proxy 0.63.3, close **T3 Code · Kilo**, refresh detection and prepare or open it again. Your private chats are retained. Preparation records the installed desktop and backend source digests, so an in-place rebuild with the same version also requires preparation again. These structural checks catch incompatible settings or isolation contracts; they cannot guarantee compatibility with every future upstream change.

T3 builds using Orchestrator V2 have their own database migration. On first opening the private Kilo workspace, T3 makes a one-time copy of its V1 database into a separate V2 database. The V1 database is retained, but new chats and changes do not synchronize between them. This applies only to Kilo's private workspace; the launcher does not open your regular T3 data directory. Use web/mobile clients compatible with the installed T3 server protocol.

## Assign a model pack

In the desktop app, choose **Models → Packs** or **My packs**, open **Use in…**, and select **T3 Code**. Preparation uses that pack instead of the shared library for the private T3 workspace. Selecting T3 Code again returns it to the shared library. Other apps keep their own assignments.

The saved T3 account and model filters still apply. Choose **All shared models** for a Kilo account to expose the complete assigned pack; official-model filters keep their existing behavior. T3 supports at most 32 models per account. Close the private T3 workspace and reopen it from Kilo Proxy after changing the assignment or editing the pack. Your normal T3 workspace and native provider accounts stay separate.

## Open the workspace

1. In Kilo Proxy, open **Agents → T3 Code · Kilo**.
2. Select **Open T3 Code · Kilo**. Kilo Proxy prepares the connections with the saved T3 settings. It starts the proxy when a Kilo connection is enabled; native-only accounts use their own login and catalog.
3. Create a new chat in T3 Code and select an agent.

On the T3 Code settings page, **Advanced → Prepare without opening** performs the same preparation without starting T3 Code. The local web interface also offers a **T3 Code · Kilo** integration tab.

Your regular T3 Code workspace can remain open. Its preferences, projects and chat history are separate and are not imported. The normal agents in the Kilo workspace refer to the usual CLI homes so they use the existing CLI login; Kilo Proxy does not copy login data or sign you out.

If the environment that starts Kilo Proxy sets an absolute `CODEX_HOME` or `CLAUDE_CONFIG_DIR`, the normal agent uses that home instead of `~/.codex` or `~/.claude`. These paths are recorded per agent; they are not applied to the Kilo agents. Changing them requires preparing again with the private T3 workspace closed. With no `CLAUDE_CONFIG_DIR` override, the native Claude agent leaves its config path unset so Claude Code retains its default login namespace. Explicit Claude config and `CLAUDE_SECURESTORAGE_CONFIG_DIR` overrides retain their original spelling, including an explicitly empty secure-storage override; they are not copied to the Kilo Claude profile. After updating from 0.67.0, close the private T3 workspace and prepare it again to migrate the managed native Claude entry.

Close **T3 Code · Kilo** before changing shared models or the proxy connection, then open it again from Kilo Proxy. Only the private Kilo workspace must be closed. This prevents changing an agent configuration while a chat is using it.

## Models, reasoning and chats

Use **Harnesses → T3 Code → gear → Accounts and models** in Kilo Proxy to enable the direct Codex and Claude connections independently, enable either Kilo connection, and select its model scope. Direct connections retain their CLI's own catalog and login. Other native T3 providers are opt-in and appear only when the installed T3 build includes their server driver.

**Kilo Proxy · Claude** defaults to **Claude Opus 5.5, Claude Sonnet 5.5 and Claude Fable 5.1**. These retain T3's native model identities and thinking controls. A dedicated local Messages route maps each identity to its exact prepared gateway ID. Selecting Sonnet cannot silently send Opus, and a model outside the prepared selection is rejected. The general Messages API and other clients are unchanged. Compatible saved reasoning defaults remain per model; levels selected for a turn remain under T3's native controls.

Choose **All shared models** to expose additional models selected in Kilo Proxy's **Models** page. Models outside T3's native Claude catalog can have fewer controls; listing them does not establish protocol, tool or reasoning support. Codex retains the reasoning levels advertised for each effective model.

Preparation creates a private Desktop asset view that filters both the Kilo model picker and its Models settings to the prepared selection. Native model objects and thinking capabilities are retained. New models discovered by the CLI remain excluded from Kilo until explicitly selected and prepared; they do not invalidate an existing workspace. Direct accounts retain their full native catalogs. Duplicate disabled native Codex/Claude rows are hidden when their managed direct account is present, retaining existing provider IDs and chats. Original application files are verified and left unchanged. Independently connected web/mobile clients retain their own picker preferences.

In **Harnesses → T3 Code → gear → Accounts and models**, OpenCode, Cursor, Grok, Antigravity, Muse Code and Pi are optional providers. Only drivers supported by the detected T3 package can be enabled. Cursor, Grok, Antigravity and Pi retain their own authentication and catalogs. Muse offers a separate connection choice, described below.

For OpenCode, choose **Own OpenCode account** or **Kilo Proxy · shared models** in the same gear page, independently of **OpenCode 1** or **OpenCode 2**. Your own account uses OpenCode’s catalog and login. The Kilo connection prepares the models saved in Kilo **Models** in a separate profile and uses the local proxy. Merely enabling OpenCode does not connect it to Kilo: existing preferences retain the own-account choice until you explicitly change it. The two connections keep distinct T3 account IDs, so changing the choice does not convert existing native chats into Kilo chats. Close the private T3 workspace and prepare it again after changing this choice; then select the intended account and model in T3.

OpenCode is optional independently of the standalone OpenCode harness. Both connections use the executable selected for their OpenCode major in Kilo’s executable settings, including a verified managed copy or an explicit custom path. An unavailable selected executable blocks preparation and opening rather than falling back to another installation. Each Kilo OpenCode major has its own profile. Native OpenCode 2 also has a separate HOME and XDG profile so it cannot migrate the normal OpenCode 1 database; it may require its own login. Kilo does not run both versions against the same state. Unsupported T3 driver/isolation contracts or an incorrect CLI major version produce an explicit preparation error.

Muse Code uses the Muse executable selected in **Settings → CLI installations**, including Kilo's verified current copy or an explicit custom path. Choose **Kilo Proxy** or **Own Meta account** beside Muse in this gear page. Newly enabling Muse selects Kilo; previously saved native Muse choices remain native. The Kilo connection uses the saved models chosen by its Muse model policy in its separate account/profile. The Meta connection keeps its own login and official catalog separate from Kilo's gateway. The installed T3 build must include a compatible native Muse driver and SDK; the chosen connection must satisfy its installed compatibility and isolation contract. The Meta catalog is discovered through Muse; the Kilo catalog comes from your saved **Models** selection. Close and prepare the private T3 workspace again after changing the executable. See [Muse Code setup](muse-code.md).

Muse Kilo has its own **Muse Kilo models** policy: **Meta models** selects saved models with original Kilo publisher IDs under `meta/` or `meta-llama/`, while **All shared models** keeps the full saved library. New Muse selections default to Meta; existing Kilo Muse selections keep their prior full library until changed. This policy does not change the native Meta account or the libraries used by other accounts. Close the private workspace and prepare again after changing it.

In T3 builds using Orchestrator V1, a chat keeps its agent and native session. To switch between a normal agent and a Kilo agent, create a new chat. Model changes within a compatible agent follow T3 Code's own session and resume behavior.

T3 builds using the supported Orchestrator V2 contract can switch agents between turns in the same chat. Each normal or Kilo agent keeps its own CLI home and connection. T3 hands off a bounded summary when switching providers; previous reasoning, tool results and attachments are not transferred. Use a new chat when you need to keep work completely separate.

The Orchestrator V2 contract resolves Claude thinking through T3's native catalog. Kilo therefore keeps native Claude identities in the picker and CLI while translating them only at its scoped HTTP endpoint. Update Claude Code when the installed T3 model catalog requires a newer CLI. Exact gateway spelling, model availability and supported thinking levels are checked independently; no family-wide fallback changes the selected model.

## Troubleshooting

- **T3 Code was not found:** install the Desktop application, then use **Check again** on the Harnesses list. Installing a CLI or the source repository alone is not the Desktop installation.
- **Incompatible T3 settings contract:** update T3 or Kilo Proxy and prepare again. A new channel or version is not rejected merely for its version number; an incompatible private-workspace, provider, secret-store or model-picker structure is rejected before changing private settings.
- **Codex or Claude Code was not found:** install the missing CLI and refresh detection. On Windows, the Microsoft Store Codex app and Claude Desktop are separate from the CLI executables used here.
- **Normal agent needs login:** sign in using that CLI normally. Avoid entering provider credentials into the Kilo agent configuration.
- **Close T3 Code · Kilo first:** quit the private Kilo T3 window, then prepare or open again. Your regular T3 Code window can remain open.
- **Shared models changed while preparing:** review the refreshed model list and open again. Preparation uses a saved snapshot so a concurrent edit cannot silently launch a different model selection.
- **“Choose one gateway ID per Claude family/version” when opening T3 Code:** older Kilo Proxy versions prepared Claude from the entire shared library. Update to 0.63.3 or newer, use **Harnesses → T3 Code → gear → Accounts and models → Claude Kilo models → Opus 5.5, Sonnet 5.5 and Fable 5.1**, close the private T3 workspace and prepare again. This keeps the shared library and Codex selection unchanged; it does not require reinstalling the CLIs. In **All shared models** mode, retain one exact gateway ID per native Claude family/version for that selection. Kilo rejects ambiguous destinations rather than silently substituting another ID.
- **A custom model is listed but requests fail:** inspect the proxy request to identify the provider response. The Codex route requires Responses support; Claude Code requires Anthropic Messages support. Tool and reasoning support depend on the model and gateway.
- **Proxy credentials changed:** close the private T3 workspace and reopen it from Kilo Proxy so the prepared agents receive the current local connection.

The separate workspace stores only the local proxy connection for the Kilo agents. Upstream Kilo and ChatGPT account credentials remain managed by Kilo Proxy. Do not put the Kilo connection in T3 Code's global environment: it would also affect the normal agent options.
