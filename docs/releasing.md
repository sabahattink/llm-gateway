# Release Process

Releases must be created from a clean, current `main` branch after CI succeeds.

## Prepare

1. Move the entries under `Unreleased` in `CHANGELOG.md` into a versioned
   section with the release date.
2. Run the local validation commands from `CONTRIBUTING.md`.
3. Commit and push the release preparation.
4. Wait for the `CI` workflow to pass on that exact commit.

## Publish

Create and push an annotated Semantic Versioning tag:

```bash
git switch main
git pull --ff-only
git status --short
git tag -a v1.1.0 -m "LLM Gateway v1.1.0"
git push origin v1.1.0
```

The `Release` workflow verifies that the tag points to the current remote
`main`, runs race tests and the vulnerability scan, and publishes archives for
Linux, macOS, and Windows with `SHA256SUMS`.

Do not move or reuse an existing release tag. If publication fails, fix the
workflow on `main`, create a new patch version, and publish a new tag.
