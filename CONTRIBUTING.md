# Contributing

Thank you for helping improve LLM Gateway.

## Development

Requirements:

- Go 1.25 or newer
- Docker for container validation

Before opening a pull request, run:

```bash
gofmt -w .
go vet ./...
go test ./...
go build ./cmd/gateway
docker build -t llm-gateway:local .
```

Keep changes focused, document user-visible behavior, and add tests for bug fixes
or new routing, provider, authentication, and storage behavior.

## Pull Requests

- Explain the problem and the chosen solution.
- Describe operational or compatibility impact.
- Include verification steps.
- Do not commit API keys, generated databases, or local environment files.

## Security

Do not report security vulnerabilities in a public issue. Follow
[SECURITY.md](SECURITY.md) instead.
