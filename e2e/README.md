# Optional browser UI end-to-end tests

Install Go (the version in `go.mod`), Node.js 22 or newer, and the pinned browser dependencies:

```sh
npm ci
npx playwright install --with-deps chromium webkit
npm run test:e2e
```

The suite runs in Chromium and WebKit. It compiles the Go test binary once, then starts a fresh Go process for each test. That process serves the production embedded UI, authenticated admin API, profile writers, and inference proxy. A local synthetic Kilo server supplies models, public ranking/benchmark metadata, organizations, JSON responses, and SSE usage. Application API responses are never intercepted by Playwright.

Each fixture uses a temporary profile root and an in-memory credential store. It neither edits the developer's editor profiles nor sends generation requests to Kilo. Clipboard payloads are captured at the browser API boundary so both browser engines can assert the copied values. Native clipboard, window, menu, and external-link behavior require the separate native desktop smoke checks.

Coverage includes:

- Manual credentials, team selection, gateway checks, proxy start/stop, and forgetting credentials.
- English/Spanish switching and persistence, client tabs and keyboard navigation, and a narrow mobile viewport.
- Independent Codex Desktop and CLI profiles, short names, reasoning metadata, and launch commands.
- Claude model picker, provider aliases, configuration preservation, and reload.
- OpenCode/Zed multi-model selection, initial models, context limits, short names, save/load, backups, and copied configuration.
- Shared Code Mode Rank, Coding Index, Speed, Price and Name sorting in every helper, including the three Xcode variants. Checks retain custom names, initial models and prepared configurations, verify English/Spanish labels, and capture desktop/mobile control layouts.
- Dynamic lab filtering combined with search, selected-only views and sorting; lab choices survive empty results and tab changes. Manual models contribute labs, `~anthropic` shares the Anthropic filter, and exact saved IDs remain unchanged.
- Actual JSON/SSE traffic through the proxy, conversation spend, cache percentages, activity bodies, redacted credentials, capture pause/clear, and accounting retention.
- Missing admin credentials and cross-origin mutation rejection.

The tests do not prove that every upstream model supports every client protocol, and do not launch the external editors. Browser E2E validates the UI/backend integration; native smoke checks validate each packaged OS application.

Lab filtering runs in a separate case for each client. Sorting cases also isolate each Xcode variant. A short navigation test verifies that lab and sort preferences survive all client tabs, while manual-model profile persistence has its own fixture. Layout cases each cover one helper type, language, and viewport with one capture. This keeps every case bounded on slower CI browsers without reducing the assertions or increasing timeouts.

Failures produce `test-results/` screenshots, traces, and Go fixture logs. Open the HTML report with `npx playwright show-report`. For a focused run, use `npm run test:e2e -- --project=webkit --grep 'OpenCode'`.
