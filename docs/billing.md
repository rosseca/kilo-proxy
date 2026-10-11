# Account balance, billed usage and local history

**Activity** separates three sources of information:

| View | Source | Scope |
| --- | --- | --- |
| Remaining balance | Kilo account API | Shared credits of the selected organization, or personal credits when no organization is selected |
| Your billed usage | Kilo usage analytics | The signed-in user's charges for the selected account: today, yesterday and the last 30 calendar days, including usage outside this proxy |
| Local observed usage | Usage fields on responses passing through Kilo Proxy | This computer's observed inference costs, token counts and cache reuse, retained across restarts |

These amounts must not be added together. In particular, BYOK provider inference costs can differ from the amount charged to the Kilo credit pool. A team balance is not a personal allowance: other members can spend from it, and organization policy, model restrictions and individual limits can still prevent a request.

## Account data

The backend uses the existing individual Kilo credential. It sends `Authorization: Bearer <Kilo credential>` and, for a selected team, `X-KiloCode-OrganizationId`. The credential never reaches the balance UI or browser JavaScript.

- `GET /api/profile/balance` returns the balance in dollars. Zero and negative balances are valid values; an authentication or network error is not displayed as zero.
- `GET /api/trpc/usageAnalytics.getTable` returns daily aggregates in a `result.data` envelope. Requests use `organizationId`, `viewAs: self`, `costSource: cost`, `granularity: day`, no dimension grouping, and a 30-day UTC interval. Personal queries additionally stay in `personal-only` scope. The app converts `costMicrodollars` with integer arithmetic and also reads requests, input/output tokens, cache read/write tokens and error counts.
- The remote **today** and **yesterday** cards use **UTC**, matching Kilo's daily buckets. The current day is partial. Kilo analytics may lag the latest request.
- Account data is refreshed in the background approximately every minute while the application UI or tray is active. **Refresh balance and usage** requests a refresh immediately. Neither operation performs model inference or generates a charge.
- Balance and usage fail independently. Missing permissions, rejected credentials, rate limits and malformed responses remain visible. Previous values are marked stale on failure or after two minutes without a successful refresh. Switching credentials or organization immediately removes the previous account's values and rejects late responses from its old requests.
- Account responses are held in memory, not written to the local history file. Requests have bounded timeouts and sizes; redirects are refused. These account APIs follow Kilo's current implementation and may change.

**Settings → Preferences → Account balance** shows the K icon plus the remaining amount on macOS. Windows uses the icon tooltip and tray menu; Linux also shows a title where supported by the desktop. An unavailable or stale balance is a dash, with its status in the menu/tooltip. **K icon** and **Session cost** remain available.

Official API references: [Kilo OpenAPI schema](https://api.kilo.ai/api/openapi.json) and [Kilo gateway profile implementation](https://github.com/Kilo-Org/kilocode/blob/main/packages/kilo-gateway/src/api/profile.ts).

### Other analytics available from Kilo

The current public schema also exposes `usageAnalytics.getSummary` (cost, requests, tokens, cache, errors, cancellations, free/BYOK counts and latency), `getTimeseries` (time series with an optional dimension), and `getBreakdown` (aggregates by model, provider, feature, mode, project, user or organization). Common filters include dates, models and projects; `costSource: market` represents a different cost basis from the billed `cost` used here. These additional breakdowns are not currently displayed in Kilo Proxy.

The schema supports team-wide queries separately, subject to permissions. Kilo Proxy requests only the signed-in user's usage and does not enumerate other members. The documented endpoints do not establish a reliable remaining personal daily allowance, a retention guarantee or a maximum analytics delay. If Kilo returns a coarser time bucket, the daily view reports that it is unavailable instead of inventing a daily split. See [Kilo usage and billing](https://kilo.ai/docs/gateway/usage-and-billing).

## Local daily history

`usage-history.json` is stored beside `settings.json` in Kilo Proxy's configuration folder. It is updated automatically after requests complete using an atomic background save. No prompt, response body, header, model name or conversation ID is included. Only daily counters and costs are persisted, with a one-way hash separating each credential and organization. The file uses private permissions on Unix and inherits the user profile's protection on Windows.

The local **today**, **yesterday** and **last seven days** use this computer's calendar date at request completion. The first recorded timestamp is shown: local history cannot reconstruct usage before this version started recording, while Kilo's remote history may include it. Up to 366 recorded dates per credential/team are retained, with at most 64 such scopes. Changing API keys starts a separate local history scope, even for the same person. The original process-session and conversation counters remain in memory and reset when the process exits.

Unknown response costs remain unknown, with priced/request coverage shown. Interrupted or limited responses may have incomplete usage. A damaged or unreadable history file is preserved and reported instead of being silently overwritten. Save failures are also reported; current in-memory totals remain available and the next completed request retries saving.

Disabling or clearing debug captures does not stop accounting or clear daily history. To remove saved local usage, quit Kilo Proxy and remove `usage-history.json` from its configuration directory. This does not delete Kilo's account records.

## Optional request capture

Request capture is **off by default**, including when upgrading from versions that captured automatically. Turn on **Activity → Capture request details** when debugging. The choice is saved in `settings.json`; up to 30 completed requests are retained in memory only while enabled.

Turning capture off clears completed entries and discards in-flight debug buffers. Re-enabling it does not resurrect earlier requests. Clearing captures leaves the option enabled but invalidates older in-flight captures. Cost and cache aggregates continue independently. See [capture redaction and size limits](security-and-debugging.md#activity-inspector).

## Validation

Tests use the real local handlers with a simulated Kilo API, covering exact decimals, negative/zero/missing balance, explicit user/team scope, UTC dates, errors, stale data, account changes, privacy, concurrent history writes, restart persistence and local calendar boundaries. Browser and native tests cover the account views and saved tray preference.

The official schema and live endpoint authentication were checked during development. The saved local credential returned HTTP 401, so this check did **not** establish a successful account balance/history response. A valid sign-in is required for a live account check; CI deliberately does not contain user credentials.
