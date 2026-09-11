# Open Design

Connect Open Design's **OpenAI API** provider to Kilo Proxy on the same computer. Kilo Proxy starts the saved connection and opens an installed desktop app; you enter the provider fields inside Open Design once.

The native helper uses exact IDs from your shared **Models** library. It does not import provider settings into Open Design, synchronize its model list, or apply shared display names, reasoning preferences and token limits there. Open Design controls its active model and settings.

## Set up the provider

1. Install [Open Design for macOS or Windows](https://github.com/nexu-io/open-design/releases/latest) separately. Configure your Kilo account and team, then choose your shared models in Kilo Proxy.
2. Open **Agents → Open Design → Set up connection**. Copy the displayed **Base URL**, **local API key** and initial model's exact ID. The default URL is `http://127.0.0.1:8877/v1`; use the displayed URL if you changed the port.
3. Click **Launch Open Design** in the connection helper. This starts the saved proxy before opening the app. If the proxy cannot start, the app stays closed and Kilo Proxy shows the error. You can also use **Start proxy** and open Open Design yourself.
4. In Open Design's settings, open **Models & providers → API providers** and choose **OpenAI**. The provider card is headed **OpenAI API**. Then choose **Provider preset → Custom provider**. Select OpenAI first: the Custom provider tab inherits the current protocol.
5. Paste the local key into **API key** and the local URL into **Base URL**. Under **Model**, select **Custom (type below)…** and paste the exact ID into **Custom model id**. A short display name from Kilo Proxy is not a model ID.
6. Click **Test** and check the result. Settings save automatically; wait for **All changes saved** before leaving. Then try a request from Open Design and check Kilo Proxy's **Activity**.

Copy Kilo Proxy's local key, never your upstream personal Kilo credential. Open Design requires a nonempty key for this OpenAI route, and Kilo Proxy authenticates it before forwarding requests. Keep the proxy running while using Open Design. Repeat the relevant provider edits if the local port or key changes.

The steps and English labels above were verified against Open Design **v0.22.2**, commit [`73953213a6fec2c8092e8e77d229a3074aa828a9`](https://github.com/nexu-io/open-design/tree/73953213a6fec2c8092e8e77d229a3074aa828a9). Its [settings implementation](https://github.com/nexu-io/open-design/blob/73953213a6fec2c8092e8e77d229a3074aa828a9/apps/web/src/components/SettingsDialog.tsx#L4470) defines the provider selection and autosave behavior. A later version may change labels or navigation.

## Models and capabilities

Choose a model supported by Kilo's **Chat Completions** API and permitted by your organization. To switch models, copy another exact shared-library ID and select it inside Open Design. Its **Fetch models** action can retrieve the proxy's full catalog; Kilo Proxy's selected library does not restrict that catalog. Changing the shared default does not change Open Design's saved model. The optional browser helper has independent model selections, as described in [shared models](shared-models.md#optional-browser-helper).

BYOK can generate responses and artifacts through Open Design's API workflow, but its [documented UI limitation](https://github.com/nexu-io/open-design/blob/73953213a6fec2c8092e8e77d229a3074aa828a9/apps/web/src/i18n/locales/en.ts#L465) excludes reading, writing and editing project files. Use Open Design's **Local CLI** mode with a separately configured CLI when you need those capabilities. This helper does not configure Local CLI, image generation or other media providers, and opening the app does not hand off Kilo Proxy's project folder.

The connection uses streaming `POST /v1/chat/completions` with a bearer key; model discovery uses `GET /v1/models`. Open Design explicitly permits loopback provider URLs, so this integration needs no internal-network exception. See the pinned [chat route](https://github.com/nexu-io/open-design/blob/73953213a6fec2c8092e8e77d229a3074aa828a9/apps/daemon/src/routes/chat.ts#L1032), [model discovery](https://github.com/nexu-io/open-design/blob/73953213a6fec2c8092e8e77d229a3074aa828a9/apps/daemon/src/integrations/provider-models.ts) and [provider URL validation](https://github.com/nexu-io/open-design/blob/73953213a6fec2c8092e8e77d229a3074aa828a9/apps/daemon/src/connectionTest.ts#L104).

## Desktop detection and Linux

Kilo Proxy detects these installed locations:

| Platform | Supported application location |
| --- | --- |
| macOS | `/Applications/Open Design.app`, then `~/Applications/Open Design.app` |
| Windows | `%LOCALAPPDATA%\Programs\Open Design\Open Design.exe` |
| Linux | Automatic desktop launch is unavailable; run Open Design from source separately. |

After installing, use **Options → Refresh detection** on its agent card. If your app is installed elsewhere, start the proxy and open it yourself. The helper opens your ordinary Open Design installation without creating a separate namespace or profile.

Open Design v0.22.2 has no official prebuilt Linux desktop artifact. Follow the project's [run-from-source instructions](https://github.com/nexu-io/open-design/tree/73953213a6fec2c8092e8e77d229a3074aa828a9#-run-from-source), start Kilo Proxy, and enter the same BYOK fields. There is no `od` launch command in this helper: that name also belongs to the system octal-dump utility on some systems.

The Open Design daemon and Kilo Proxy must run on the same host for these loopback URLs. A remote Open Design instance, hosted website or container with a separate network namespace cannot reach your computer's proxy through its own `127.0.0.1`.

## Saved settings and verification

Open Design saves BYOK settings in its renderer's `open-design:config` localStorage entry. Its daemon's `app-config.json` API handles other preferences, including Local CLI settings, and cannot import these BYOK fields. Kilo Proxy leaves both stores to Open Design. See the pinned [renderer persistence](https://github.com/nexu-io/open-design/blob/73953213a6fec2c8092e8e77d229a3074aa828a9/apps/web/src/state/config.ts#L1025) and [daemon field allowlist](https://github.com/nexu-io/open-design/blob/73953213a6fec2c8092e8e77d229a3074aa828a9/apps/daemon/src/app-config.ts#L147).

Source compatibility and a successful application launch do not prove paid model access or organization billing. Model discovery does not run inference; Open Design's **Test** or a conversation can make a model request. Use the result and Kilo Proxy's observed request details to verify the model and connection you selected.
