# Synara

Kilo Proxy opens **Synara · Kilo**, a separate workspace with two Codex accounts and two Claude Code accounts:

| Agent | Connection |
| --- | --- |
| Codex · Normal | Existing Codex CLI login |
| Kilo Proxy · Codex | Shared Kilo/ChatGPT models through the local proxy |
| Claude · Normal | Existing Claude Code login |
| Kilo Proxy · Claude | Compatible shared models through the local proxy |

The **Kilo Proxy** names and green account indicators distinguish the Kilo accounts. Your regular Synara workspace and its chats stay separate and can remain open.

## Requirements

- Install [Synara Beta 1.0.0-beta.1](https://github.com/Emanuele-web04/synara/releases/tag/v1.0.0-beta.1), the validated Desktop release. Other versions need validation before their private configuration can be prepared. See Synara's requirements for [Codex](https://www.trysynara.com/docs/providers/codex) and [Claude Code](https://www.trysynara.com/docs/providers/claude-code).
- Install native **Codex CLI** and **Claude Code CLI**. Desktop app login is separate from CLI login. The normal agents require an existing CLI session.
- **Codex · Normal** requires file-based Codex authentication: this Synara release does not support Codex's `keyring` or `auto` credential-store modes. Kilo Proxy does not convert or copy your credentials.
- Connect Kilo Proxy and save 1–32 shared models in **Models**. Choose the initial model and any compatible reasoning defaults there.

## Open the workspace

1. Open **Agents → Synara · Kilo** in Kilo Proxy.
2. Select **Open Synara · Kilo**. Kilo Proxy prepares the four accounts and starts the proxy before opening Synara.
3. Start a new chat, choose its account and check the selected model.

**Integration settings → Prepare without opening** performs the same preparation without launching Synara. The local web interface also has a **Synara · Kilo** tab.

Close only **Synara · Kilo** before changing the shared models or proxy connection, then reopen it from Kilo Proxy. Its app data and Kilo CLI profiles are private. The normal accounts refer to your usual CLI homes; Kilo Proxy does not read or copy their login credentials or change the normal account. Synara's own CLI drivers may write session metadata and link `auth.json` into a normal Codex overlay, or copy that file if linking fails.

## Models and reasoning

The prepared list shows your shared default first and keeps exact gateway IDs. For **Kilo Proxy · Claude**, select an ID from that list, such as `anthropic/claude-opus-4-6`. A built-in Claude choice maps to the exact gateway ID when its family and version match one prepared model. Other built-in choices can remain unavailable through your gateway; their presence does not establish that they work through Kilo.

Synara's custom Claude model picker has no effort selector. For custom gateway IDs, compatible reasoning defaults come from Kilo Proxy's **Models**. Close and reopen the Kilo workspace after changing them. Saved per-model levels require **Claude Code 2.1.251 or newer**. Explicit effort selected for a built-in Claude choice follows Synara's driver behavior. Automatic and unsupported Claude levels do not receive a native effort override. Generation, tools and available reasoning levels depend on the model, CLI and provider.

An existing chat keeps its account. Create a new chat to switch between a normal account and its Kilo counterpart; changing the account for a new chat does not convert earlier chats.

## Troubleshooting

- **Synara or a CLI was not found:** install the supported Desktop app, native Codex CLI and Claude Code, then use **Options → Refresh detection**.
- **A Claude model is unavailable:** check that the composer selected the exact prepared gateway ID rather than a built-in Claude entry.
- **Close Synara · Kilo first:** quit its private window and prepare again. Your normal Synara window can remain open.
- **Models or credentials changed:** close and reopen the private workspace so the prepared profiles receive the current settings.
- **Windows environment overrides:** before opening Synara, Kilo Proxy checks the variable names in the registry's user and machine Environment scopes without reading their values or changing the registry. Synara can restore these global overrides into its process, so saved provider, development or `SYNARA_*` variables block opening the private workspace. Remove those overrides from Windows **Environment Variables** and try again. If the inspection cannot be completed, Synara is not opened.

Only the local proxy connection is stored for the Kilo accounts. Upstream Kilo and ChatGPT credentials remain managed by Kilo Proxy.
