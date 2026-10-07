# Synara

Kilo Proxy opens **Synara · Kilo**, a separate workspace with two Codex accounts and two Claude Code accounts:

| Agent | Connection |
| --- | --- |
| Codex · Normal | Existing Codex CLI login |
| Kilo Proxy · Codex | Shared Kilo/ChatGPT models through the local proxy |
| Claude · Normal | Existing Claude Code login |
| Kilo Proxy · Claude | Compatible shared models through the local proxy |

The **Kilo Proxy** names and green account indicators distinguish the Kilo accounts. Your regular Synara workspace and its chats stay separate and can remain open.

You can disable accounts you do not use in Synara's provider settings. Disabling **Claude · Normal** does not invalidate **Kilo Proxy · Claude** or require its normal CLI login to work.

## Requirements

- Install [Synara Beta](https://github.com/Emanuele-web04/synara/releases). From Kilo Proxy **0.56.2**, the installed Beta version is not restricted; the detected version is shown for information. See Synara's requirements for [Codex](https://www.trysynara.com/docs/providers/codex) and [Claude Code](https://www.trysynara.com/docs/providers/claude-code).
- Install native **Codex CLI** and **Claude Code CLI**. Desktop app login is separate from CLI login. The normal agents require an existing CLI session.
- **Codex · Normal** requires file-based Codex authentication: Synara does not support Codex's `keyring` or `auto` credential-store modes. Kilo Proxy does not convert or copy your credentials.
- Connect Kilo Proxy and save 1–32 shared models in **Models**. Choose the initial model and any compatible reasoning defaults there.

## Open the workspace

1. Open **Agents → Synara · Kilo** in Kilo Proxy.
2. Select **Open Synara · Kilo**. Kilo Proxy prepares the four accounts and starts the proxy before opening Synara.
3. Start a new chat, choose its account and check the selected model.

**Integration settings → Prepare without opening** performs the same preparation without launching Synara. The local web interface also has a **Synara · Kilo** tab.

Close only **Synara · Kilo** before changing the shared models or proxy connection, then reopen it from Kilo Proxy. Its app data and Kilo CLI profiles are private. The normal accounts refer to your usual CLI homes; Kilo Proxy does not read or copy their login credentials or change the normal account. Synara's own CLI drivers may write session metadata and link `auth.json` into a normal Codex overlay, or copy that file if linking fails.

## Models and reasoning

The prepared list shows your shared default first and keeps exact gateway IDs. For **Kilo Proxy · Claude**, select an ID from that list, such as `anthropic/claude-opus-4-6`. A built-in Claude choice maps to the exact gateway ID when its family and version match one prepared model. Other built-in choices can remain unavailable through your gateway; their presence does not establish that they work through Kilo.

Synara's custom Claude model picker has no effort selector. For custom gateway IDs, compatible reasoning defaults come from Kilo Proxy's **Models**. Close and reopen the Kilo workspace after changing them. Saved per-model levels require **Claude Code 2.1.251 or newer**; Opus/Sonnet 5.5 defaults require **2.1.267 or newer**. Explicit effort selected for a built-in Claude choice follows Synara's driver behavior. Automatic and unsupported Claude levels do not receive a native effort override. Generation, tools and available reasoning levels depend on the model, CLI and provider.

An existing chat keeps its account. Create a new chat to switch between a normal account and its Kilo counterpart; changing the account for a new chat does not convert earlier chats.

## Delegating to another account

On macOS, the private workspace's Synara tools list the four accounts separately, with their enabled/authentication status and their own model catalog. Choose **Codex · Normal**, **Kilo Proxy · Codex**, **Claude · Normal** or **Kilo Proxy · Claude** when asking an agent to create another chat. The tools retain that account's `instanceId` for single and batch creation. A request that omits an ambiguous account, selects a disabled account or uses a model outside its catalog fails before creating the chat; Synara does not silently substitute a different account.

The private macOS runtime checks the backend integration points before applying this correction, preserving the installed app and the regular workspace. If a Beta build changes those integration points, the private launch reports the incompatibility before starting Synara. Account-aware tool delegation has not been validated for the native Windows or Linux desktop yet.

**Delegar a otra cuenta:** indica Codex Normal, Codex Kilo, Claude Normal o Claude Kilo al pedir que se abra otro chat. Cada cuenta tiene su catálogo y estado propios. Si falta elegir la cuenta o está desactivada, la petición se detiene antes de crear el chat. Esta corrección de las herramientas de delegación está validada para el workspace privado en macOS.

## Voice notes

Synara transcribes voice notes through a ChatGPT-authenticated **Codex · Normal** account, including when the chat uses **Kilo Proxy · Claude**. Keep Codex Normal enabled and sign in through `codex login` using file-based authentication. A Kilo API key or Kilo Proxy's own ChatGPT connection does not provide this Synara voice session. Allow Synara to access the microphone when prompted.

The private Codex Normal adapter makes the CLI's ChatGPT login status readable by Synara. Codex still verifies its real ChatGPT session before Synara sends audio for transcription; the adapter does not read or copy login credentials.

**Notas de voz:** Synara transcribe usando **Codex · Normal** autenticado con ChatGPT, incluso si el chat usa **Kilo Proxy · Claude**. Mantén Codex Normal activado, inicia sesión con `codex login` y autenticación en archivo, y concede acceso al micrófono. La clave de Kilo y la conexión ChatGPT de Kilo Proxy no sustituyen esa sesión. Puedes desactivar **Claude · Normal** sin bloquear Claude Kilo.

## Troubleshooting

- **Synara or a CLI was not found:** install Synara Beta, native Codex CLI and Claude Code, then use **Options → Refresh detection**.
- **A Claude model is unavailable:** check that the composer selected the exact prepared gateway ID rather than a built-in Claude entry.
- **Close Synara · Kilo first:** quit its private window and prepare again. Your normal Synara window can remain open.
- **Models or credentials changed:** close and reopen the private workspace so the prepared profiles receive the current settings.
- **Windows environment overrides:** before opening Synara, Kilo Proxy checks the variable names in the registry's user and machine Environment scopes without reading their values or changing the registry. Synara can restore these global overrides into its process, so saved provider, development or `SYNARA_*` variables block opening the private workspace. Remove those overrides from Windows **Environment Variables** and try again. If the inspection cannot be completed, Synara is not opened.

Only the local proxy connection is stored for the Kilo accounts. Upstream Kilo and ChatGPT credentials remain managed by Kilo Proxy.
