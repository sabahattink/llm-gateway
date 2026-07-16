# Reddit Launch Draft

## Title options

- I built a lightweight self-hosted LLM gateway in Go that gives you one OpenAI-compatible endpoint across providers
- Open source: LLM Gateway – one OpenAI-compatible endpoint for OpenAI, Claude, Gemini, Groq, Ollama, LM Studio, vLLM, and more
- I wanted to switch LLM providers without changing app code, so I built this

## Post body

I have been working on **LLM Gateway**, an open source self-hosted LLM gateway written in Go.

The main idea:

- one OpenAI-compatible chat completions endpoint
- multiple providers behind it
- easier provider switching without changing application code

Supported providers:

- OpenAI
- Anthropic
- Gemini
- Groq
- Mistral
- Cohere
- xAI
- Perplexity
- Together AI
- Ollama
- LM Studio
- vLLM

What it includes right now:

- OpenAI-style request format
- SSE streaming in OpenAI format
- built-in web UI for setup and provider settings
- request logs and basic usage / cost analytics
- SQLite storage
- Docker or single-binary deployment

It is aimed at developers who want something smaller and simpler to self-host, especially if they already use the OpenAI SDK and want to compare hosted and local providers behind one endpoint.

Repo:
`https://github.com/sabahattink/llm-gateway`

Happy to get feedback on:

- missing providers
- SDK compatibility edge cases
- self-hosting UX
- what would make this more useful in production

## Short version

If your app already talks to an OpenAI-compatible endpoint, you can point it at LLM Gateway and switch providers by changing the model name.
