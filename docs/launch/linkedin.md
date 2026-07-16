# LinkedIn Launch Draft

## Post

We have open sourced **LLM Gateway**.

LLM Gateway is a lightweight, self-hosted LLM gateway written in Go that gives developers:

- one OpenAI-compatible endpoint
- support for multiple providers behind that endpoint
- a simpler way to switch providers without rewriting application code

It currently supports OpenAI, Anthropic, Gemini, Groq, Mistral, Cohere, xAI, Perplexity, Together AI, Ollama, LM Studio, and vLLM.

Why we built it:

- many teams want to compare providers without changing SDK integrations
- local and hosted models often need to live behind the same API
- a lot of projects want a smaller self-hosted footprint than a larger orchestration stack

What is in the repo today:

- OpenAI-compatible chat completions endpoint
- OpenAI-format streaming
- built-in admin UI for setup, provider settings, logs, and analytics
- Docker and single-binary deployment
- SQLite-based storage

Repository:
`https://github.com/sabahattink/llm-gateway`

If you work on developer infrastructure, LLM apps, or self-hosted AI tooling, I would appreciate feedback on the onboarding flow, provider coverage, and compatibility expectations.

## Shorter version

We open sourced **LLM Gateway**: a Go-based self-hosted gateway that lets developers use one OpenAI-compatible endpoint across OpenAI, Claude, Gemini, Groq, Ollama, LM Studio, vLLM, and more.
