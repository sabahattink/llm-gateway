# LLM Gateway

One self-hosted, OpenAI-compatible API for Claude, GPT, Gemini, Groq, Ollama,
and other major LLM providers. Switch models without rewriting application
code.

[![CI](https://github.com/sabahattink/llm-gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/sabahattink/llm-gateway/actions/workflows/ci.yml)
[![GitHub Release](https://img.shields.io/github/v/release/sabahattink/llm-gateway?style=flat-square)](https://github.com/sabahattink/llm-gateway/releases)
[![Go 1.26](https://img.shields.io/badge/go-1.26-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/github/license/sabahattink/llm-gateway?style=flat-square)](LICENSE)

<p align="center">
  <img src="docs/screenshots/dashboard-dark.png" alt="LLM Gateway dashboard" width="860">
</p>

```bash
git clone https://github.com/sabahattink/llm-gateway.git
cd llm-gateway
cp .env.example .env
# Set LLM_GATEWAY_API_KEY in .env before exposing the gateway.
docker compose up --build
```

---

## Works with any OpenAI SDK

Your app already speaks OpenAI. Point it at the gateway, change the model name, done.

```python
import os

from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8080/v1",
    api_key=os.environ["LLM_GATEWAY_API_KEY"],
)

# Claude
client.chat.completions.create(model="claude-sonnet-4-6", messages=[...])

# Gemini
client.chat.completions.create(model="gemini-2.0-flash", messages=[...])

# Local (Ollama)
client.chat.completions.create(model="llama3", messages=[...])
```

Same client. Same code. Any provider. Works with the OpenAI SDK in Python, Node.js, Go, Ruby, or any language.

```bash
# What it looks like over the wire
$ curl http://localhost:8080/v1/chat/completions \
    -H "Authorization: Bearer $LLM_GATEWAY_API_KEY" \
    -H "Content-Type: application/json" \
    -d '{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"Hello"}]}'

# Response — standard OpenAI format
# X-LLM-Provider: anthropic
# X-LLM-Latency-Ms: 843
{"id":"...","choices":[{"message":{"role":"assistant","content":"Hello! How can I help you today?"}}],...}

# Change the model, get a different provider — nothing else changes
$ curl http://localhost:8080/v1/chat/completions \
    -H "Authorization: Bearer $LLM_GATEWAY_API_KEY" \
    -H "Content-Type: application/json" \
    -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}]}'

# X-LLM-Provider: openai
# X-LLM-Latency-Ms: 612
{"id":"...","choices":[{"message":{"role":"assistant","content":"Hi there! What can I do for you?"}}],...}
```

---

## Supported Providers

**Cloud** — OpenAI · Anthropic · Google Gemini · Groq · Mistral · Cohere · xAI · Perplexity · Together AI

**Local** — Ollama · LM Studio · vLLM

The gateway resolves the provider from the model name automatically — no routing config needed.

---

## Why LLM Gateway?

- **One endpoint, twelve providers** — `/v1/chat/completions` works for all of them
- **Model-based routing** — `claude-*` goes to Anthropic, `gemini-*` to Google, `llama*` to Groq or Ollama
- **Unified streaming** — OpenAI-format SSE across all providers, including real-time conversion for Anthropic and Gemini
- **Single binary, zero dependencies** — Go + SQLite. No Redis. No Postgres. No sidecars.
- **API keys encrypted at rest** — AES-256-GCM with per-key random nonces
- **Built-in observability** — every request logged with tokens, latency, cost, and provider
- **Admin UI included** — settings, live dashboard, and analytics without extra tooling

---

## Quick Start

### Docker

```bash
docker volume create llm-gateway-data
docker run --name llm-gateway \
  --restart unless-stopped \
  --read-only \
  --tmpfs /tmp \
  --security-opt no-new-privileges:true \
  -p 8080:8080 \
  -e LLM_GATEWAY_API_KEY="$LLM_GATEWAY_API_KEY" \
  -v llm-gateway-data:/data \
  ghcr.io/sabahattink/llm-gateway:v1.1.0
```

1. Open `http://localhost:8080`
2. Set your admin password
3. Add provider API keys in **Settings**
4. Send authenticated requests to `http://localhost:8080/v1/chat/completions`

Configuration is environment-based; no YAML or external database is required.

To build locally instead, clone the repository, copy `.env.example` to `.env`,
set `LLM_GATEWAY_API_KEY`, and run `docker compose up --build`.

> **Remote setup:** if you deploy on a server first, use the one-time token printed at startup:
> ```
> Remote setup URL: /admin/setup?token=<token>
> ```

### Build From Source

```bash
git clone https://github.com/sabahattink/llm-gateway.git
cd llm-gateway
go build -o llm-gateway ./cmd/gateway
./llm-gateway
```

Release archives report their embedded version:

```bash
./llm-gateway --version
```

### Streaming

```bash
curl -N http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $LLM_GATEWAY_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "stream": true,
    "messages": [{"role": "user", "content": "Count to five"}]
  }'
```

---

## Admin UI

### Live Dashboard

<p align="center">
  <img src="docs/screenshots/dashboard-dark.png" alt="Dashboard" width="860">
</p>

Monitor usage, cost, and latency across all providers — total requests, tokens, errors, average latency, and a live request feed with provider, model, status, and latency per row.

### Analytics

<p align="center">
  <img src="docs/screenshots/analytics-dark.png" alt="Analytics" width="860">
</p>

Daily and monthly trends. Per-provider token usage. Per-model cost breakdown. CSV export.

### Provider Settings

<p align="center">
  <img src="docs/screenshots/settings-dark.png" alt="Settings" width="860">
</p>

Configure all 12 providers in one place. Test connections before saving. Keys are masked in the UI and encrypted in the database.

---

## Model Routing Reference

| Pattern | Provider |
|---|---|
| `gpt-*`, `o1`, `o3-mini` | OpenAI |
| `claude-*` | Anthropic |
| `gemini-*` | Google Gemini |
| `llama*`, `mixtral-*` | Groq |
| `mistral-*`, `codestral*` | Mistral |
| `command-*` | Cohere |
| `grok-*` | xAI |
| `sonar-*` | Perplexity |
| `org/model` (slash in name) | Together AI |
| anything else | Ollama → LM Studio → vLLM |

---

## LLM Gateway vs LiteLLM

If LiteLLM feels like too much infrastructure, LLM Gateway is for you.

| | LLM Gateway | LiteLLM |
|---|---|---|
| Runtime | Go binary | Python service |
| Storage | SQLite (embedded) | Postgres + Redis |
| Extra services | None | Separate dashboard, proxy |
| Admin UI | Built-in | Separate container |
| Provider coverage | 12 | 100+ |
| Deployment | `docker run` | `docker-compose` with multiple services |
| Best fit | Simple self-hosted gateway | Enterprise routing, virtual keys, policy |

---

## Configuration

```bash
# Server
PORT=8080
DB_PATH=gateway.db
PUBLIC_RATE_LIMIT_RPM=60          # 0 disables rate limiting

# Security
LLM_GATEWAY_ENCRYPTION_KEY=       # auto-generated if unset
LLM_GATEWAY_API_KEY=              # Bearer token for /v1/chat/completions; 32+ characters
LLM_GATEWAY_TRUST_PROXY_HEADERS=false

# Cloud providers
OPENAI_API_KEY=
ANTHROPIC_API_KEY=
GOOGLE_API_KEY=
GROQ_API_KEY=
MISTRAL_API_KEY=
COHERE_API_KEY=
XAI_API_KEY=
PERPLEXITY_API_KEY=
TOGETHER_API_KEY=

# Local providers
OLLAMA_ENABLED=false
OLLAMA_BASE_URL=http://localhost:11434
LMSTUDIO_ENABLED=false
LMSTUDIO_BASE_URL=http://localhost:1234
VLLM_ENABLED=false
VLLM_BASE_URL=http://localhost:8000
```

### Docker Compose

```yaml
services:
  gateway:
    build: .
    ports:
      - "8080:8080"
    environment:
      LLM_GATEWAY_API_KEY: ${LLM_GATEWAY_API_KEY:-}
    volumes:
      - gateway-data:/data
    restart: unless-stopped
    read_only: true
    tmpfs:
      - /tmp
    security_opt:
      - no-new-privileges:true

volumes:
  gateway-data:
```

---

## API Reference

| Method | Path | Auth | Description |
|---|---|---|---|
| `POST` | `/v1/chat/completions` | Bearer* | OpenAI-compatible proxy |
| `GET` | `/health` | No | Status and registered providers |
| `GET` | `/admin` | Yes | Dashboard |
| `GET` | `/admin/analytics` | Yes | Analytics |
| `GET` | `/admin/settings` | Yes | Provider settings |
| `GET` | `/api/dashboard` | Yes | Dashboard JSON |
| `GET` | `/api/stats` | Yes | 24 h aggregated stats |
| `GET` | `/api/logs` | Yes | Recent request logs |
| `GET` | `/api/stats/daily` | Yes | Daily stats (up to 365 days) |
| `GET` | `/api/stats/monthly` | Yes | Monthly stats (up to 36 months) |
| `GET` | `/api/stats/providers` | Yes | Per-provider token breakdown |
| `GET` | `/api/stats/models` | Yes | Per-model cost and token usage |

Responses include `X-LLM-Provider` and `X-LLM-Latency-Ms` headers.

`*` Bearer authentication is enabled when `LLM_GATEWAY_API_KEY` is set. Set it
to a random value of at least 32 characters before exposing the gateway.

---

## Security

- **Passwords** — bcrypt cost 12
- **API keys** — AES-256-GCM, unique nonce per key, encrypted in SQLite
- **Gateway API** — optional constant-time Bearer-token authentication; required for internet-facing deployments
- **Sessions** — 32-byte random tokens, SHA-256 hashed in DB, 24 h expiry
- **Rate limiting** — per-IP token bucket on all public endpoints
- **Proxy headers** — `X-Forwarded-For` trust disabled by default
- **First-run** — password required; remote access needs one-time startup token

### Password Reset

```bash
./llm-gateway --reset-password
# or
docker exec <container> llm-gateway --reset-password
```

---

## Architecture

```
cmd/gateway/main.go          server bootstrap, provider registration
internal/
  proxy/router.go            request routing, streaming dispatch, request logging
  providers/
    registry.go              model → provider resolution (prefix + exact match)
    interface.go             Provider interface, OpenAI request/response types
    streaming.go             SSE helpers, real-time format conversion
    openai.go                OpenAI + compatible backends (Groq, Mistral, Perplexity,
                             xAI, Together AI, Ollama, LM Studio, vLLM)
    anthropic.go             Anthropic native adapter + stream translation to OpenAI SSE
    gemini.go                Gemini native adapter + stream translation to OpenAI SSE
    cohere.go                Cohere chat_history adapter
  admin/
    auth.go                  setup, login, sessions, lockout
    handler.go               dashboard, analytics, settings APIs
  middleware/
    ratelimit.go             per-IP token bucket
    cost.go                  per-model cost estimation
    logging.go               HTTP request logging
  storage/sqlite.go          WAL-mode SQLite, AES-256-GCM key encryption
web/                         admin UI (dashboard, analytics, settings, login, setup)
```

---

## Contributing and Security

See [CONTRIBUTING.md](CONTRIBUTING.md) for the development workflow. Report
security issues privately as described in [SECURITY.md](SECURITY.md).
Release changes are recorded in [CHANGELOG.md](CHANGELOG.md); maintainers use
the documented [release process](docs/releasing.md).
Community participation is governed by the
[Code of Conduct](CODE_OF_CONDUCT.md).

---

## License

[MIT](LICENSE)
