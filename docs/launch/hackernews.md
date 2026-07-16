# Hacker News Launch Draft

## Title options

- Show HN: LLM Gateway – one OpenAI-compatible endpoint for OpenAI, Claude, Gemini, Groq, Ollama, LM Studio, vLLM, and more
- Show HN: LLM Gateway – a lightweight self-hosted OpenAI-compatible gateway in Go
- Show HN: LLM Gateway – switch LLM providers without changing your app code

## Post body

Hi HN,

I am launching **LLM Gateway**, a lightweight self-hosted LLM gateway written in Go.

The goal is simple:

- expose one OpenAI-compatible chat completions endpoint
- route requests to multiple providers
- let developers switch providers without rewriting application code

It currently supports OpenAI, Anthropic, Gemini, Groq, Mistral, Cohere, xAI, Perplexity, Together AI, Ollama, LM Studio, and vLLM.

What I wanted from this project:

- a single binary or simple Docker deployment
- no extra services beyond SQLite
- OpenAI SDK compatibility for chat completions
- built-in admin UI for provider settings, logs, and basic cost analytics

It is not trying to be the broadest orchestration layer. The focus is a small, practical gateway for people who want one stable API in front of multiple hosted or local model providers.

Repository:
`https://github.com/sabahattink/llm-gateway`

Would especially appreciate feedback on:

- provider coverage and gaps
- compatibility expectations from OpenAI SDK users
- where the setup or onboarding still feels rough

## Suggested first comment

Some implementation details that may be relevant:

- Go service with SQLite storage
- OpenAI-style `/v1/chat/completions` endpoint
- OpenAI-format SSE streaming
- built-in web UI for setup, settings, dashboard, and analytics
- supports both hosted providers and local backends like Ollama / LM Studio / vLLM

If you try it, the fastest path is Docker:

```bash
git clone https://github.com/sabahattink/llm-gateway.git
cd llm-gateway
docker compose up --build
```
