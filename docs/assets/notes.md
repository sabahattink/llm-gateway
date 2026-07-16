# Visual Asset Notes

Existing screenshots already in the repo:

- `docs/screenshots/dashboard-dark.png`
- `docs/screenshots/analytics-dark.png`
- `docs/screenshots/settings-dark.png`

Recommended manual assets to add before launch:

- terminal demo screenshot showing Docker startup and the first successful `curl` request
- short GIF or MP4 showing: `docker run` -> setup -> add provider key -> first request
- setup/login screenshot so the first-run flow is visible in the README or social posts
- provider-switching demo image showing the same client request routed to different models/providers
- optional architecture diagram for docs or launch threads

If only two new assets are added, prioritize:

1. **`docs/screenshots/demo.png`** — terminal demo screenshot for a future README update
2. short startup-to-first-request GIF

## demo.png spec

Expected content:
- Dark terminal window
- A `curl` command sent to `http://localhost:8080/v1/chat/completions`
- Response JSON visible
- Response headers visible: `X-LLM-Provider: anthropic` and `X-LLM-Latency-Ms: 843`
- Ideal size: 1800×900px or similar wide-format screenshot
