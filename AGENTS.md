# Project conventions

- Use only the production Sprites API: `https://api.sprites.dev`. Loopback HTTP servers are allowed for isolated tests; do not call staging APIs.
- Label every newly created Sprite with `app:sprite-tunnel`. Do not add a purpose label unless explicitly requested. Preserve unrelated labels on existing Sprites.
- `install` uses an existing Sprite. Never create cloud resources from ordinary unit tests or pull-request workflows.
- Keep test credentials out of the repository and logs. Delete disposable Sprites after live verification.
- Run `make check` before publishing. Release tags are immutable and must match a CHANGELOG entry.
- Use `gh` for GitHub operations. CI and release Actions are pinned to commit SHAs.
