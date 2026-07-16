# Changelog

All notable changes to LLM Gateway are documented in this file.

The project follows Semantic Versioning.

## Unreleased

## 1.1.0 - 2026-07-16

### Added

- Bearer authentication for the public chat-completions endpoint.
- Hardened container runtime checks in CI.
- Automated cross-platform release archives and SHA-256 checksums.
- Integration coverage for native and OpenAI-compatible providers.
- Versioned cross-platform binaries for Linux, macOS, and Windows.

### Changed

- Updated the supported toolchain to Go 1.26.5.
- Hardened the runtime container with a non-root user and read-only filesystem.
- Improved streaming transport control and malformed-event handling.
- Corrected SQLite analytics time filtering and login lockout reset behavior.

### Fixed

- Restored immediate SSE flushing through HTTP logging middleware.
- Bounded rate-limiter client state and analytics query ranges.
- Rejected empty Gemini conversations before making an upstream request.

## 1.0.0 - 2026-03-16

- First public release.
