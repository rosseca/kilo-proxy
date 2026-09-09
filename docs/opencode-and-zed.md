# OpenCode and Zed helpers

The native **OpenCode** and **Zed** cards use the same [shared model library](shared-models.md). Edit up to 50 IDs, names, a default model and token limits in **Models**. Unknown models start with a 200,000-token context assumption; review it against provider metadata. An output limit of zero is unspecified. Both integrations use Chat Completions; reasoning remains automatic, and model listing does not certify inference compatibility.

The optional browser helper remains a separate interface with independent per-client model selections and explicit profile preparation.

## OpenCode

1. Install OpenCode separately and configure Kilo Proxy in **Settings**.
2. Choose models, names, a default and any token limits in **Models**. These library changes save automatically.
3. On **Agents**, use OpenCode's **Options** to choose its project folder, then **Open OpenCode**. It prepares `~/.opencode-kilo/opencode.json` and `kilo-models.json` (`%USERPROFILE%\.opencode-kilo` on Windows), then opens an interactive terminal.
4. Use `/models` to switch models. Edit the common library and reopen the agent to apply changes. Manual preparation is under **Options → Integration settings → Prepare without launching**.

The launcher selects the profile through `OPENCODE_CONFIG`, clears an inherited inline configuration override for this child, and pins the initial model with `--model`.

The custom provider includes the local proxy key in its protected configuration, so `/connect` is unnecessary for this profile. It never includes the upstream Kilo credential. The initial model also supplies `small_model` for internal requests. Context/output limits are emitted together when an output limit is known.

This is a separate configuration file, not a complete OpenCode sandbox: global and project configurations still merge, and project settings can override this file. The helper does not change ordinary global OpenCode settings, auth storage, permissions, plugins, or project files. Check the resolved settings if a repository overrides the Kilo provider.

The optional configuration export contains the local key; keep it private. The launch command contains only the configuration path and model ID. It restores the parent shell environment on Unix and PowerShell.

## Zed

1. Edit models, names, limits and the default in **Models**.
2. On **Agents**, click **Open Zed**. It prepares the `kilo-local` OpenAI-compatible provider and Agent default model in your user settings, preserving other providers and unrelated settings, then opens Zed.
3. For the one-time key setup, open **Options → Integration settings → Copy key for Zed (one-time setup)**. In Zed, run `agent: open settings`, locate `kilo-local`, and paste the key. Zed stores it in its system keychain; the helper does not put a key in Zed's settings.
4. Select a model in Zed's Agent panel with the proxy running. Native generation still depends on the model and gateway.

Settings locations are `~/.config/zed/settings.json` on macOS/Linux, `$XDG_CONFIG_HOME/zed/settings.json` when configured on Linux, and `%APPDATA%\Zed\settings.json` on Windows. A neighboring `kilo-models.json` stores the helper selection. Reload Zed if it does not pick up a change. This configures Zed Agent, not edit prediction or external agents.

## Updates and backups

Native library edits save automatically, but generated editor files update only when that agent is opened or prepared. Reopen an existing agent if it has retained old settings; these are not live model-picker updates. Use **Models → Import an existing agent selection** to review an older saved OpenCode/Zed profile before replacing the common library. The browser helper retains its explicit Load/Prepare flow.

JSON comments, trailing commas, unrelated settings, and exact large-number literals are preserved. Only Kilo-managed fields and the selected default are updated. Changed files receive exact `.bak` backups; a no-op leaves existing backups intact. Invalid or duplicate-key JSON, non-object settings sections, symbolic-link destinations, and unsafe backups are rejected. Writes use temporary files and restore earlier writes if a later write fails; this is not a crash-atomic transaction. If your settings use symlinks, use the optional export to merge the configuration yourself.

The saved model selections contain no credentials. Files written by Kilo Proxy use private file permissions on Unix and inherit the profile directory's ACL on Windows. Do not publish generated OpenCode profiles or backups containing local credentials.

## Verified coverage

- Automated Go tests cover multi-model validation, JSONC preservation, exact backups, idempotent saves, updates, unsafe paths, and profile load/save.
- Native checks cover shared-library propagation, automatic saving, project persistence and preparation-to-open transitions. Browser checks separately cover its independent tabs, names, initial models, copying, dirty-state handling, English/Spanish and narrow layouts.
- The installed **OpenCode 1.4.0** loaded both generated models and completed a streaming request against a synthetic local gateway with the expected local bearer key. Internal requests stayed within the selected Kilo provider. No paid Kilo inference was performed.
- Zed is not installed in this development environment. Generated settings and the helper are tested; native Zed generation remains unverified.

Repeat the optional installed-client check with:

```sh
KILO_TEST_OPENCODE=/absolute/path/to/opencode go test -run TestInstalledOpenCode -v -count=1
```

This uses temporary config/data/cache directories and a synthetic local gateway, without Kilo credentials. It may download public OpenCode/provider dependencies.

References: [OpenCode configuration precedence](https://opencode.ai/docs/config/), [OpenCode custom providers](https://opencode.ai/docs/providers/#custom-provider), [Zed API access and key storage](https://zed.dev/docs/ai/use-api-access), and [Zed settings](https://zed.dev/docs/reference/all-settings).
