# ChatGPT subscription connection

Kilo Proxy can connect to ChatGPT alongside your Kilo account. Both connections use the same native model library and agent profiles. Choose a model to choose the connection: `chatgpt/<slug>` uses ChatGPT subscription quota, while existing Kilo IDs such as `openai/<slug>` keep using Kilo credits. There is no global provider switch and no automatic fallback to Kilo or the paid OpenAI API when ChatGPT rejects a request or reaches a limit.

This is an **experimental direct connection** to the subscription backend used by public Codex clients, with protocol behavior also informed by OpenCode and Oh My Pi. It is not the paid OpenAI API or a guaranteed, stable API for arbitrary clients. Account eligibility, model access, limits and backend behavior can change independently of Kilo Proxy. Connecting an account does not guarantee access to every model.

## Connect and choose models

1. Stop the proxy before changing ChatGPT authentication. In the first-run connection page or **Settings → Connection**, choose **Sign in with ChatGPT**.
2. Enter the displayed device code on `https://auth.openai.com/codex/device` and complete authorization. The app updates when sign-in finishes; **Cancel ChatGPT login** ends a pending attempt.
3. Open **Models** and add models labelled **ChatGPT**. The catalog comes from the connected account. You can keep Kilo and ChatGPT models together, including models with the same underlying slug.
4. Save your default, names and supported preferences in the shared library, then open an agent. Reopen an existing agent after changing its selected models so its generated profile is refreshed.

ChatGPT can be used without a Kilo connection. You can also add Kilo later, or disconnect either account independently. Disconnecting ChatGPT does not remove Kilo credentials or erase the shared model library; a saved ChatGPT model needs a connected ChatGPT account to run.

The existing agent helpers continue to prepare their own profiles with the local proxy URL and local API key. Agents do not receive ChatGPT OAuth tokens. The native interface shares one model library; the optional browser helper retains its existing per-client selections. See [client setup](clients.md), [terminal commands](terminal-commands.md), and the additional [Claude Desktop compatibility requirements](claude-desktop.md).

## Credentials and privacy

Sign-in uses OpenAI's device authorization and token endpoints. Kilo Proxy does not read or import credentials from an existing Codex, OpenCode or OMP installation. It manages token refresh for its own connection.

OAuth credentials are encrypted with AES-256-GCM in `chatgpt-credentials.enc` inside Kilo Proxy's configuration directory. Only a random 32-byte encryption key is stored in a separate operating-system credential-store entry. File replacement is atomic; temporary files contain ciphertext, not plaintext tokens. The ciphertext is tied to that credential-store entry, so copying the file alone does not transfer the connection. Disconnecting removes this connection's encrypted file and key; failures are reported so you can retry.

Subscription requests go directly to ChatGPT with the account's subscription authentication. They do not receive Kilo credentials or its organization header. Generated client profiles contain only the local proxy credential. Existing [request-capture privacy limits](security-and-debugging.md) still apply: known authentication secrets are redacted, but arbitrary secrets inside conversation content are not automatically detected.

## Quota, tokens and money

**Subscription usage** shows the primary and secondary quota windows returned by ChatGPT, including used percentages and reset times when available. These values describe the subscription account, including usage outside Kilo Proxy. They are independent of Kilo credit, Kilo account billing and local request counts. Missing values remain unavailable; they are not treated as zero.

The app refreshes quota periodically while open, and **Refresh subscription usage** requests a fresh reading. A reading older than five minutes is marked out of date. Offline or unavailable quota checks do not themselves disconnect the account.

In **Settings → Appearance**, select **ChatGPT quota** to show the remaining percentage of the **primary** window in the menu bar or tray tooltip. An unavailable or stale reading shows `—`. The secondary window remains visible in the usage panel; there is no setting to choose it for the tray. The existing **Kilo balance** and **Session cost** display choices remain separate.

Activity records subscription requests and any returned token/cache usage. Subscription token counts are not converted to a dollar charge and do not count as missing Kilo price reports. Local daily subscription totals are kept separately from Kilo's account history. Models from this catalog do not carry an invented per-token price.

## Protocol and client limits

The upstream connection uses Responses with `store=false` and streaming. Kilo Proxy adapts Chat Completions and Anthropic Messages clients to that protocol, including text, supported image inputs, client function calls and returned tool results. Tools still run in the original agent; this does not start a second Codex agent to execute them.

- Each request must include the needed conversation history. A nonempty `previous_response_id` is rejected because server-side conversation storage is disabled.
- Caller-specified output-token budgets, `temperature` and `top_p` are not supported by this subscription transport and are removed before forwarding. A local profile's limits do not guarantee an upstream generation budget.
- Supported reasoning effort comes from the account's model catalog and the client adapter. Models that require a code-only tool mode are excluded from the catalog.
- This is not full API equivalence. For example, the Chat Completions/Messages adapters reject unsupported audio output, stop sequences, log probabilities and multiple response choices instead of silently emulating them.
- Image inputs depend on the selected model. ChatGPT requests do not use Kilo Proxy's Kilo image-upload pipeline. The local 32 MiB request limit still applies.
- The optional image-generation MCP has its own **Kilo / ChatGPT** provider selector. Choose **ChatGPT subscription · Experimental** in **Models → Image generation** to use the connected subscription's built-in image tool. Click **Save image settings**; no Codex profile, separate image-model ID or OpenAI API key is needed. Reopen your agent to apply it. The image connection is independent of the conversation model; it never falls back to Kilo credits. Generated originals stay local and editing accepts previously generated files. See [image setup and limits](codex-images.md).

Errors or exhausted quota stay on the chosen connection. To change which account pays or supplies quota, explicitly select a model from the other connection.

## Validation scope

Automated tests use synthetic authentication, quota, catalog and inference servers. They cover encrypted storage, refresh concurrency, logout races, connection isolation, model routing, protocol conversion, streaming, tools and usage accounting. Opt-in tests also run locally installed Codex CLI, Claude Code, OpenCode and OMP against a **mock local subscription backend**, using temporary profiles and a read-only file/tool round trip; recorded requests exercise their real client formats in regression tests.

Live validation on September 28, 2026 used a separately authorized ChatGPT account and its `chatgpt/gpt-5.5` model. Device sign-in, credential reload after restarting, catalog access and quota retrieval succeeded. Responses, Chat Completions and Anthropic Messages each completed a real tool-call/result round trip without streaming to the caller. Claude Code 2.1.276, OpenCode 1.18.30, Oh My Pi 18.3.1 and Codex CLI 0.154.0 also completed live streaming turns, reading an unpredictable value from a temporary file and returning it in their final answer. These checks used temporary client profiles and no Kilo inference or paid fallback.

Regression tests cover two observed subscription behaviors: SSE responses without a `Content-Type` header, and terminal responses with an empty `output` after completed output items have already streamed. The latter items are retained when assembling nonstreaming replies.

To repeat the installed-client live check, explicitly set `KILO_CHATGPT_LIVE_CLIENTS` (a comma-separated selection of `claude-full,opencode,omp,codex`), `KILO_CHATGPT_LIVE_URL` (the local proxy origin without `/v1`), `KILO_CHATGPT_LIVE_MODEL` and `KILO_CHATGPT_LIVE_KEY`, then run `go test -run '^TestChatGPTLiveClients$' -count=1 -v`. Supply the local key privately through the environment, never a committed file or shared command. This opt-in test consumes subscription quota; ordinary test runs skip it.

Generated-profile tests cover the other desktop/editor integrations, but those applications have not all been exercised live with this subscription. A passing check for one model and account does not establish compatibility with every agent, model, account or desktop release.
