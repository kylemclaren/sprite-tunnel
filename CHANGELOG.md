# Changelog

## v0.1.1

- Keep setup and connection in one live terminal display, without borders or duplicate panels.
- Share a port with `sprite-tunnel 3000`; relay setup is automatic.
- Reuse the Sprite CLI login, OS keyring/file-backed credentials, and directory selection.
- Use `--public` or `--private` for access, and `--sprite NAME` to override selection.
- Create missing Sprites with the `app:sprite-tunnel` label and reuse working relays.
- Discover the bundled Linux relay automatically in Homebrew and standalone downloads.
- Authenticate private named-Sprite client/status requests using the existing login.
- Verified live sharing with CLI credentials, private/public access, labelled provisioning, and relay reuse.

## v0.1.0

Initial release.

- Reverse HTTP tunnels over binary WebSockets and yamux, including HTTP streaming and WebSocket upgrades.
- Relay, client, install, status, and one-command bootstrap.
- Terminal dashboard with animated spinners, installation stages, connection history, and plain-output fallback.
- `--url-auth public|sprite` controls Sprite URL access and supports private-ingress authentication.
- Idempotent relay installation, protected secrets, automatic reconnect, and explicit client replacement.
- Production-only Sprites API access and `app:sprite-tunnel` labels for newly provisioned Sprites.
- Linux, macOS, and Windows builds for amd64 and arm64, with SHA-256 checksums.

### Notes

- The installer targets Linux/amd64 Sprites. On macOS, Windows, or Linux/arm64, provide the Linux/amd64 relay binary with `--binary`, or run from source with Go installed.
- Live production tests passed for HTTP, concurrent requests, WebSocket echo, installation, and both URL authentication modes. The exact ingress idle-timeout limit remains unconfirmed.
