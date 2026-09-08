# OpenCode and Zed helpers

Both tabs provide a searchable model picker with prices, up to 50 selected models, editable names, an initial model, and adjustable context/output limits under **Advanced options**. Each client has independent selections. Unknown models start with a 200,000-token context assumption; review it against provider metadata. Output limits left at zero are unspecified. These helpers use Chat Completions and do not invent reasoning settings or certify model compatibility.

## OpenCode

1. Install OpenCode separately, then start Kilo Proxy and its proxy.
2. Open **OpenCode**, check models, edit their short names, and choose **Use on startup**.
3. Click **1. Prepare profile**. The helper creates or updates `~/.opencode-kilo/opencode.json` and `kilo-models.json` (`%USERPROFILE%\.opencode-kilo` on Windows).
4. Copy the launch command for your shell and run it from your project directory. It selects the profile through `OPENCODE_CONFIG`, clears an inherited inline configuration override for this child, and pins the initial model with `--model`.
5. Use `/models` to switch models. Load the saved selection to edit it later, then prepare again.

The custom provider includes the local proxy key in its protected configuration, so `/connect` is unnecessary for this profile. It never includes the upstream Kilo credential. The initial model also supplies `small_model` for internal requests. Context/output limits are emitted together when an output limit is known.

This is a separate configuration file, not a complete OpenCode sandbox: global and project configurations still merge, and project settings can override this file. The helper does not change ordinary global OpenCode settings, auth storage, permissions, plugins, or project files. Check the resolved settings if a repository overrides the Kilo provider.

The optional configuration export contains the local key; keep it private. The launch command contains only the configuration path and model ID. It restores the parent shell environment on Unix and PowerShell.

## Zed

1. Open **Zed** in Kilo Proxy and select your models, names, limits, and initial model.
2. Click **1. Prepare profile**. It updates the `kilo-local` OpenAI-compatible provider and the Agent default model in your user settings, preserving other providers and unrelated settings.
3. Click **2. Copy key for Zed**. In Zed, open `agent: open settings`, locate `kilo-local`, and paste the key once. Zed stores it in its system keychain; this helper does not put a key into Zed's settings.
4. Select a model in Zed's Agent panel and test a conversation with the proxy running.

Settings locations are `~/.config/zed/settings.json` on macOS/Linux, `$XDG_CONFIG_HOME/zed/settings.json` when configured on Linux, and `%APPDATA%\Zed\settings.json` on Windows. A neighboring `kilo-models.json` stores the helper selection. Reload Zed if it does not pick up a change. This configures Zed Agent, not edit prediction or external agents.

## Updates and backups

**Load saved selection** restores models and names; prepare again to apply the current proxy port/key. The helper marks edited selections as unsaved and disables its ready-to-use copy action until preparation succeeds. Unsaved panel edits are not persisted automatically.

JSON comments, trailing commas, unrelated settings, and exact large-number literals are preserved. Only Kilo-managed fields and the selected default are updated. Changed files receive exact `.bak` backups; a no-op leaves existing backups intact. Invalid or duplicate-key JSON, non-object settings sections, symbolic-link destinations, and unsafe backups are rejected. Writes use temporary files and restore earlier writes if a later write fails; this is not a crash-atomic transaction. If your settings use symlinks, use the optional export to merge the configuration yourself.

The saved model selections contain no credentials. Files written by Kilo Proxy use private file permissions on Unix and inherit the profile directory's ACL on Windows. Do not publish generated OpenCode profiles or backups containing local credentials.

## Verified coverage

- Automated Go tests cover multi-model validation, JSONC preservation, exact backups, idempotent saves, updates, unsafe paths, and profile load/save.
- Browser checks cover both tabs, names, initial models, copying, independent selections, dirty-state handling, English/Spanish, and mobile layouts.
- The installed **OpenCode 1.4.0** loaded both generated models and completed a streaming request against a synthetic local gateway with the expected local bearer key. Internal requests stayed within the selected Kilo provider. No paid Kilo inference was performed.
- Zed is not installed in this development environment. Generated settings and the helper are tested; native Zed generation remains unverified.

Repeat the optional installed-client check with:

```sh
KILO_TEST_OPENCODE=/absolute/path/to/opencode go test -run TestInstalledOpenCode -v -count=1
```

This uses temporary config/data/cache directories and a synthetic local gateway, without Kilo credentials. It may download public OpenCode/provider dependencies.

References: [OpenCode configuration precedence](https://opencode.ai/docs/config/), [OpenCode custom providers](https://opencode.ai/docs/providers/#custom-provider), [Zed API access and key storage](https://zed.dev/docs/ai/use-api-access), and [Zed settings](https://zed.dev/docs/reference/all-settings).
