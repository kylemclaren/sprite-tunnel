# Changelog

## v0.1.0

Initial release.

- Reverse HTTP tunnels over binary WebSockets and yamux, including HTTP streaming and WebSocket upgrades.
- Relay, client, install, status, and one-command bootstrap.
- Charm dashboard with animated spinners, installation stages, connection history, and plain-output fallback.
- `--url-auth public|sprite` controls Sprite URL access and supports private-ingress authentication.
- Idempotent relay installation, protected secrets, automatic reconnect, and explicit client replacement.
- Production-only Sprites API access and `app:sprite-tunnel` labels for newly provisioned Sprites.
- Linux, macOS, and Windows builds for amd64 and arm64, with SHA-256 checksums.

### Notes

- The installer targets Linux/amd64 Sprites. On macOS, Windows, or Linux/arm64, provide the Linux/amd64 relay binary with `--binary`, or run from source with Go installed.
- Live production tests passed for HTTP, concurrent requests, WebSocket echo, installation, and both URL authentication modes. The exact ingress idle-timeout limit remains unconfirmed.
