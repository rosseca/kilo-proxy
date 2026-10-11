# Subscription client request fixtures

These requests were recorded from unmodified local clients on 2026-09-28:

| Client | Version | Protocol |
| --- | --- | --- |
| Claude Code (default tool catalog) | 2.1.276 | Anthropic Messages |
| OpenCode | 1.18.30 | Chat Completions |
| Oh My Pi | 18.3.1 | Responses |
| Codex CLI | 0.154.0 | Responses |

Each client received a synthetic read-only tool call through the real Kilo
Proxy handlers, read a temporary sentinel file, returned its contents, and
consumed the final reply. A localhost mock supplied all model responses. No
subscription quota, real credentials, personal profiles, or external inference
services were used. The Claude run retained its default system prompt and full
tool catalog, but only the `Read` tool was allowed and requested.

The fixtures contain the request after the tool result. Temporary paths, system
prompts, user-context boilerplate, telemetry identifiers, and tool descriptions
are removed or replaced. Tool schemas, content structure, IDs, cache hints, and
request options are preserved. Regular CI replays them with
`TestChatGPTRecordedClientRequests`.

Run the optional checks against already installed clients:

```sh
KILO_CHATGPT_CLIENTS=claude-full,opencode,omp,codex \
  go test -run '^TestChatGPTInstalledClients$' -count=1 -v
```

The runner creates temporary profiles and a temporary project, uses a clean
child environment, and points inference at localhost. External HTTP proxies
are set to an unused local port and automatic updates are disabled. Only the
synthetic file read is requested. Set `KILO_CHATGPT_CAPTURE_DIR` to a temporary
directory to export refreshed, sanitized fixture candidates for review.

These checks validate real client wire compatibility and tool execution; they
do not establish live account eligibility or access to any particular model.
