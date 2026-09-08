# Codex Desktop profile isolation

Inspected on 2026-09-07: `/Applications/ChatGPT.app`, bundle identifier `com.openai.codex`, version `26.901.51231`. This is read-only inspection of the installed application code, not modification or redistribution of it.

- `.vite/build/bootstrap-CfJ5wTIB.js` resolves `CODEX_ELECTRON_USER_DATA_PATH` before `app.setPath("userData", ...)` and the single-instance lock.
- `.vite/build/main-BT6ViFC-.js` preserves an explicit `CODEX_HOME` during login-shell environment loading when the explicit Electron user-data path is set. It also contains a separate-profile launch flow using `open -n --env CODEX_HOME=... --env CODEX_ELECTRON_USER_DATA_PATH=... <app> --args --user-data-dir=...`.
- `.vite/build/src-VqXTPopo.js` resolves the local Codex home from `process.env.CODEX_HOME`, falling back to `~/.codex`.

The generated launchers use a new Codex home and a new Electron user-data directory. Setting CODEX_HOME alone does not isolate browser/desktop state. Launching another ordinary window does not create a separate provider profile.

The [official configuration documentation](https://learn.chatgpt.com/docs/config-file/config-advanced#config-and-state-locations) documents CODEX_HOME for local configuration/state. It does not establish a stable public contract for the Electron variable above. Recheck the installed version if the second GUI opens the original window or ignores its provider.

Validation: the macOS command is executed against a mock `open` executable to verify exact arguments, whitespace/quote handling and isolation. The CLI command is executed against a mock `codex` executable to verify child environment values, rejection of missing config and preservation of the parent shell environment. PowerShell restoration is checked structurally. These are not claims of an end-to-end desktop run or successful Kilo inference. No main-profile auth, cookies or conversation databases are copied.

The installed app bundle is not included in Kilo Proxy distributions. Install it separately from the vendor and select its actual path. The normal app retains its normal updates. Profiles isolate settings and history; they do not sandbox repositories shared between instances.


## Multiple model picker (0.6)

The official `model_catalog_json` configuration option selects a JSON catalog at startup. Kilo Proxy exports `{ "models": [...] }`, with each entry using the Kilo ID as `slug`, a display name, `visibility: "list"`, `supported_in_api: true`, priority and conservative capabilities. It supplies its own generic coding instructions; it does not distribute the installed application's prompts. Context and image input are populated only from available Kilo metadata. Known exact model IDs have provider/Codex fallback reasoning presets. IDs without published effort variants or fallback presets default to no levels and can be configured manually. The native picker receives `supported_reasoning_levels` and `default_reasoning_level`; model IDs remain unchanged. Explicit efforts from Kilo `opencode.variants` now take precedence over fallback presets. Gateway acceptance still depends on the route.

Run `python3 scripts/check-codex-catalog.py /absolute/path/to/codex` to test an installed binary. This creates an isolated temporary CODEX_HOME, generates both files through the same frontend functions, starts app-server on stdio, initializes it and checks model/list. It uses a non-listening localhost provider and never starts a thread or inference. Tested with the application's bundled codex-cli 0.153.4 on 2026-09-07: both entries are visible; a relative models.json resolves against the config directory; model/list's default follows catalog order. The exporter therefore puts the chosen default first as well as setting the top-level model.

This proves backend catalog loading, not an end-to-end Kilo inference or automated operation of the desktop's picker. The installed desktop source uses model/list for model discovery. Models must still support the Responses API and be permitted for the selected organization. Restart the Kilo desktop instance after replacing the files.

## Reasoning and catalog editing (0.10.0)

Select catalog rows with checkboxes or select filtered results (up to 50). Per-model controls customize available efforts and the initial effort. Use suggested levels restores presets, including after loading a legacy catalog. Fable 5.1 uses low/medium/high/xhigh/max with high as default, based on [Anthropic effort documentation](https://platform.claude.com/docs/en/build-with-claude/effort). OpenAI presets use exact IDs from the installed Codex capability metadata.

Before 0.14.0, Save wrote only the isolated profile's `models.json`, atomically with a `.bak` backup. Load restores saved selections. That older flow did not rewrite `config.toml`; it required copying the generated config on first setup and when changing the initial model. Version 0.14.0 replaces that manual step as described below. Restart the isolated desktop instance after saving. The catalog must be referenced by `model_catalog_json = "models.json"`.

The installed app-server's `model/list` was checked for all selected efforts and the initial effort, without paid inference. The proxy preserves Responses `reasoning.effort`, including when adapting Anthropic tool schemas.

## Unified picker (0.10.1)

Codex Desktop now uses one model list. Check a row to include it, choose reasoning inline and click Use on startup to set the initial model. Customize levels expands advanced effort settings within that row. Selected only filters the same list, including saved and manual IDs missing from the current public catalog. The previous secondary model list and default-model dropdown are removed. Saving uses the same catalog format.

Verified in the browser in English and Spanish: bulk and individual selection, inline reasoning and initial model, custom levels without affecting model membership, save/load and manual IDs. Codex CLI retains its independent controls.

## Sol discounted and GLM fix (0.10.2)

The normalizer previously discarded Kilo's `opencode.variants` effort metadata, and the fallback table omitted Sol's discounted route and GLM 5.3. Explicit gateway efforts are now retained and preferred, with manual edits still taking precedence. Variant names and token budgets are not interpreted as effort levels.

Exact fallbacks: `openai/gpt-5.6-sol-discounted` supports none/low/medium/high/xhigh/max, initially low, matching [Kilo's public catalog](https://api.kilo.ai/api/gateway/models). `z-ai/glm-5.3` and `z-ai/glm-5.3-flash` support only low/high/max, initially max, matching [Z.ai's parameter documentation](https://docs.z.ai/guides/overview/concept-param#reasoning_effort). No model IDs are rewritten.

## Editable display names (0.10.3)

The **Display name** field (formerly **Name in Codex**) edits the selected row's display label, up to 80 characters. The exported `display_name` changes while `slug`, reasoning and initial-model identity remain unchanged. Clearing the field restores the catalog name. Search matches custom names as well as original names and IDs. Save/load preserves labels; restart Codex Kilo after saving to reload its catalog.

Validated with the installed app-server: displayName returns Sol and GLM while model retains the full Kilo IDs. Browser checks cover typing without losing focus, searching, saving/loading labels, and English/Spanish copy.

## Activity inspector (0.11.0)

Completed proxy requests can be inspected at four stages: original client request, adapted gateway request, original gateway response, and adapted client response. Capture starts enabled, can be paused, and stores at most 30 entries in memory, 128 KiB per body and 32 KiB per header set. At most 16 simultaneous requests are captured. Clear history also prevents earlier in-flight requests from restoring cleared entries. No trace is written to disk.

Authentication headers, cookies and known local/upstream/admin keys are redacted in debug copies; actual traffic is unchanged. Arbitrary secrets embedded in prompts are not automatically detected. The authenticated details endpoint is separate from lightweight state polling. SSE continues flushing while copied, and JSON formatting preserves number lexemes and duplicate keys. Header snapshots represent the proxy HTTP objects rather than a wire-level TCP capture.


## Automatic Desktop profile preparation (0.14.0)

**1. Prepare profile** creates the isolated profile directory and saves both the catalog and TOML. It updates Kilo-managed defaults and provider settings, including the current local port and the selected initial reasoning effort. If the config selects a named profile, its model defaults are synchronized too. Other settings and comments are preserved using parsed TOML expression ranges; inline tables, dotted/quoted keys and multiline values are supported. The edited result is parsed again and compared with the intended settings before writing.

Existing changed files receive exact `.bak` backups. Identical saves do not replace those backups. Destinations are validated before writing, and each file is replaced atomically; if the TOML write fails after the catalog write, the previous catalog is restored. This is not a crash-atomic transaction across two files. Invalid TOML is left untouched and reported without exposing its contents.

The helper writes only to the fixed isolated profile on its own computer, not a browser-supplied destination. It stores `env_key = "KILO_LOCAL_API_KEY"`; the existing launch command supplies the local key. Conflicting Kilo bearer/command authentication, Authorization header overrides and query parameters are removed. The original Codex profile and the separate CLI setup flow are unchanged.

Validation covers new profiles, repeated saves, exact backups, preservation of unrelated TOML, changed models/ports, selected named profiles, authentication repair, invalid settings and unsafe destinations. Browser checks exercise preparation and updates in English and Spanish. No paid inference is needed to prepare the profile.

The actual helper-written files were also loaded with the installed Codex 0.153.4 app-server in a disposable profile: model/list returned both exact IDs, short names, native reasoning levels and the chosen initial model. No thread or inference was started.
