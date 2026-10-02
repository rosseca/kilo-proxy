# OpenMausBot

OpenMausBot appears as a full-width card below Codex and Claude Desktop on **Agents**. Its desktop application connects directly to Kilo Proxy using its built-in OpenAI-compatible engine; no Codex or Claude CLI is needed.

## Open a workspace

1. Install [OpenMausBot](https://github.com/milind-soni/OpenMausBot/releases/latest) 0.1.92 or later with the compatible desktop engine, and connect Kilo or ChatGPT in Kilo Proxy.
2. Choose your shared models and default in **Models**.
3. Click **Open OpenMausBot** on **Agents**. Kilo Proxy prepares its private configuration, starts the proxy and launches the installed application. A preparation or proxy startup error leaves the application closed.
4. Complete OpenMausBot's welcome flow if shown. New bots use the **Kilo Proxy** engine and shared default; choose another shared model in OpenMausBot's picker. Existing bots retain their own model selections.

The card shows whether the desktop application is installed. If it is missing, use its download link, install it separately and refresh detection. Choose project folders inside OpenMausBot.

## Shared models and limits

The managed engine receives an explicit list of shared model IDs, including both Kilo IDs and `chatgpt/<slug>` subscription models. It sends authenticated **Chat Completions** requests to the local proxy; the ID determines which connected account serves the request. It does not silently fall back between accounts.

OpenMausBot 0.1.92's OpenAI-compatible driver displays exact model IDs. It does not accept custom display names or forward the library's reasoning levels, so those settings are not advertised as applied. Structured tools and image input remain subject to the selected model's capabilities and OpenMausBot's own permissions. This integration does not grant computer access or approve tools.

Preparation enables workspace-wide automatic compaction using a threshold bounded by the smallest selected context budget. OpenMausBot can compact earlier based on its own inferred model capacity; it does not import the proxy's per-model context metadata. This is not a per-model context or output limit, and does not remove the gateway's HTTP payload limit. Update **Settings → Large images** separately when needed.

## Changes and private files

Quit the **Kilo OpenMausBot workspace** before applying changed models, defaults, context or proxy credentials. Closing its window can leave a background process running; use OpenMausBot's quit action. An unchanged configuration can reopen the existing workspace. Kilo Proxy never quits it automatically.

Files live below `openmausbot/` in Kilo Proxy's [configuration directory](shared-models.md#saved-files):

| Path | Purpose |
| --- | --- |
| `data/config.json` | Managed engine, local connection, model list and compaction settings |
| `data/` | OpenMausBot's private bots, conversations and workspace state |
| `ui/` | Separate Electron interface data and credential storage |
| `selection.json` | Prepared shared selection and readiness metadata |

The launcher creates both private directories before starting the application and passes `OMB_DATA_DIR` and `--user-data-dir`. It does not import your normal OpenMausBot or legacy OpenGrokBot workspace. Generated settings are merged with unrelated settings, backed up when changed and saved atomically. They contain only the **local proxy key**, never your upstream Kilo key or ChatGPT tokens. Keep generated configuration and backups out of version control.

## Compatibility and tests

The integration is checked against the installed OpenMausBot **0.1.92** and its [documented custom-engine configuration](https://github.com/milind-soni/OpenMausBot/blob/main/docs/custom-engines.md). macOS, Windows and Linux desktop launch paths have automated discovery and launch-plan coverage. Live installed-client checks use an isolated workspace and synthetic gateway responses; CI does not require an OpenMausBot installation or paid model requests.
