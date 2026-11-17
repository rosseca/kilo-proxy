# Embeddings through Kilo Proxy

Kilo Proxy exposes an OpenAI-compatible embedding endpoint through the same local
URL and key as its chat API. It uses the Kilo credential and organization saved in
that profile. Both headless and desktop editions support it.

## Connect and discover models

Configure Kilo using the [headless setup guide](headless.md), then discover the
organization's current embedding models:

```sh
kilo-proxy-headless embeddings models
kilo-proxy-headless embeddings models --json
```

Use `--config-dir /absolute/path` to select another profile. Discovery contacts
the running controller or uses its saved connection while the service is stopped;
it does not start inference. Each call fetches fresh metadata. Human output shows
exact model IDs, names and published dimensions (`unknown` when unavailable).
JSON returns `defaultModel`, `models` and `aliases`, with optional model metadata
such as `dimension`, `dimensionMode`, `note` and `scoreThreshold`.

A listed model is not proof that inference will pass current organization policy,
quota or billing checks. Embedding discovery is separate from `/v1/models`, the
shared chat library and coding-agent profiles. No agent-model setup is required.

Start the service for inference, then inspect its connection in another terminal:

```sh
kilo-proxy-headless serve
# In another terminal:
kilo-proxy-headless connection
```

The default base URL is `http://127.0.0.1:8877/v1`; use the URL reported for your
profile. To reveal the **local** key explicitly, use `connection --show-key`
(optionally `--json`). Set your application's `KILO_LOCAL_API_KEY` environment
variable to that key. The saved upstream Kilo key remains private. Desktop users
can copy the URL/local key from Settings.

## Python OpenAI SDK

With the OpenAI Python package available:

```python
import os
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8877/v1",
    api_key=os.environ["KILO_LOCAL_API_KEY"],
)

result = client.embeddings.create(
    model="google/gemini-embedding-2",
    input=["First document", "Second document"],
    encoding_format="float",
)

for item in result.data:
    print(item.index, len(item.embedding))
```

Choose an exact ID from your organization's catalog. A single string is also
accepted as `input` when supported by the model. Options such as `dimensions`,
token arrays and alternative encodings depend on the upstream model. The proxy
forwards those fields without changing them. Your application owns chunking,
batch sizing, retry policy, vector storage and retrieval.

## HTTP API

`POST /v1/embeddings` forwards to Kilo's embedding API. With the local key already
in `KILO_LOCAL_API_KEY`, a small request is:

```sh
curl http://127.0.0.1:8877/v1/embeddings \
  -H "Authorization: Bearer $KILO_LOCAL_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"openai/text-embedding-3-small","input":"Example text","encoding_format":"float"}'
```

Responses retain upstream vector values, indices, model and usage fields. The
proxy does not split batches, substitute models or retry inference automatically.
`GET /v1/embedding-models` is a Kilo-specific discovery extension, available with
the same local key and returning the scoped catalog described above.

## Access, limits and errors

Embeddings require a ready Kilo connection and selected organization. A ChatGPT
subscription alone cannot serve them. Caller authorization/organization overrides
are replaced by the saved connection; existing loopback and local access checks
apply. These endpoints are for API applications, not browser-origin calls.

- Local HTTP 401 can indicate an incorrect local key or a missing Kilo connection;
  `kilo_connection_required` identifies the latter.
- `unsupported_embedding_provider` rejects `chatgpt/` subscription models.
- Upstream inference errors retain their status/body. Organization/provider
  restrictions, balance and quota remain authoritative. HTTP 429 can include
  `Retry-After` for your client to respect.
- Requests are limited locally to 32 MiB, including chunked uploads. Upstream
  services may impose smaller limits. For HTTP 413, reduce the batch/input rather
  than retrying identical oversized requests unchanged.
- Discovery returns safe status-bearing errors without falling back to a public
  catalog, another profile or anonymous credentials. Retry discovery after
  `embedding_connection_changed` if you changed the saved connection.

## Usage and BYOK

Your client receives the original upstream usage object. Local accounting records
available input tokens and reported inference cost/source, including upstream
provider cost for BYOK. Zero Kilo gateway fees do not imply free provider
inference. The proxy neither infers provider-key ownership nor adds alternative
cost components together. See [billing](billing.md).

Desktop Activity identifies embedding requests and displays input-token counts;
the inspector labels reported BYOK routing. Missing token/cost metadata remains
unknown. Large vector responses are observed without storing vectors for
accounting; malformed or oversized usage metadata can leave accounting incomplete
while successful vector responses still reach the client unchanged.

Normal persisted usage history contains no input text or vectors. Existing
explicit Activity content capture can include request/response content and is
bounded; it is separate from normal accounting.
