# sprite-tunnel

Your localhost, at your Sprite URL. One Go binary: an HTTP relay on the Sprite and an outbound client on your laptop. Includes an animated terminal dashboard, animated spinners, installation stages, connection history, and elapsed time. Pipes and `NO_COLOR=1` get plain logs.

## Releases

Download a prebuilt archive for Linux, macOS, or Windows (amd64 or arm64) from [GitHub Releases](https://github.com/kylemclaren/sprite-tunnel/releases). Each release includes `checksums.txt`. Run `sprite-tunnel --version` to see the version and source commit.

A macOS or Windows client also needs the Linux/amd64 archive for installation: extract it separately and pass its executable with `install --binary /path/to/linux/sprite-tunnel`. Building from source is the alternative.

## Quick start

Requires Go 1.26+ to build, an existing Sprite, and a Sprites API credential in `SPRITES_TOKEN` or a file supplied with `--api-token-file`.

```sh
go build -trimpath -o bin/sprite-tunnel .

# Install the relay and make the URL publicly accessible.
./bin/sprite-tunnel install --sprite my-app --url-auth public
./bin/sprite-tunnel client --sprite my-app --to localhost:3000
```

Or install and connect in one command:

```sh
./bin/sprite-tunnel client --sprite my-app --to localhost:3000 \
  --bootstrap --url-auth public
```

Switch between public and org-authenticated access while connecting:

```sh
./bin/sprite-tunnel client --sprite my-app --to localhost:3000 --url-auth public
./bin/sprite-tunnel client --sprite my-app --to localhost:3000 --url-auth sprite
```

`--url-auth public|sprite` works on **install and client**. It changes the Sprite's URL setting persistently, including after the client exits. Omit it to preserve the existing setting. Changing it requires `SPRITES_TOKEN` or `--api-token-file`; selecting `sprite` also uses that credential to authenticate the tunnel through private ingress. Visitors must independently satisfy Sprite URL authentication.

To use an already-private URL without changing its setting:

```sh
./bin/sprite-tunnel client --sprite my-app --to localhost:3000 \
  --ingress-token-file /path/to/sprites-api-token
./bin/sprite-tunnel status --sprite my-app \
  --ingress-token-file /path/to/sprites-api-token
```

## Commands

```text
relay   --listen :8080 [--secret-file PATH]
client  --sprite NAME | --url ws[s]://HOST/_tunnel --to HOST:PORT
        [--secret-file PATH] [--verbose] [--bootstrap]
        [--url-auth public|sprite] [--ingress-token-file PATH]
        [--api-token-file PATH] [--api-url URL] [--binary PATH]
install --sprite NAME [--url-auth public|sprite]
        [--api-token-file PATH] [--api-url URL] [--url URL]
        [--secret-file PATH] [--binary PATH] [--source DIR]
status  --sprite NAME | --url URL [--json] [--ingress-token-file PATH]
```

`--url` overrides resolution, even with `--sprite`. HTTP(S) base URLs are accepted and converted to WS(S). Install saves the actual API-returned Sprite URL in `~/.config/sprite-tunnel/NAME.url`; this handles organization suffixes. Without a saved URL, `--sprite NAME` defaults to `https://NAME.sprites.app` as specified. Supply the actual URL if no install has been run on this laptop.

All real Sprites operations use the production API, `https://api.sprites.dev`. `--api-url` only permits that URL or a loopback HTTP server for local tests. `--url` changes the tunnel endpoint, not the management API.

Secrets: explicit `--secret-file`, then `TUNNEL_SECRET`, then `~/.config/sprite-tunnel/NAME.secret`. Relay requires a secret file or `TUNNEL_SECRET`. Install generates 32 random bytes encoded as hex, writes the local file with mode 0600, and reuses it on subsequent installs. Keep the local secret to retain access after reinstalling.

`status --json` prints only health JSON to stdout. Client events and the dashboard go to stderr; relay access and connection logs go to stdout. Ctrl+C and SIGTERM close the tunnel cleanly. Replaced clients and unauthorized clients exit 1 without retrying.

## Local development

In two terminals, with any local HTTP application on port 3000:

```sh
TUNNEL_SECRET=local-development ./bin/sprite-tunnel relay --listen 127.0.0.1:8080
TUNNEL_SECRET=local-development ./bin/sprite-tunnel client \
  --url ws://127.0.0.1:8080/_tunnel --to localhost:3000
```

Visit `http://127.0.0.1:8080`. Use WSS for a remote relay; plain WS is intended for local development.

## Creating a Sprite

Installation uses an existing Sprite. Label newly created Sprites with `app:sprite-tunnel`:

```sh
sprite create my-app --skip-console --label app:sprite-tunnel
```

For SDK provisioning, pass `[]string{"app:sprite-tunnel"}` to `CreateSpriteWithOrg`. Do not add a purpose label or replace labels on an existing Sprite. Temporary live-test Sprites follow the same convention and are deleted after verification.

## Installation details

The installer uses `superfly/sprites-go` for Sprite lookup, file uploads, URL settings, and service creation. It:

1. Obtains the existing Sprite's real URL.
2. Uses the running binary on Linux/amd64, or cross-compiles from `--source` on other hosts. `--binary` accepts a prebuilt Linux/amd64 ELF binary, useful when shipping a macOS client without source or Go.
3. Uploads a staged executable and installs it atomically at `/usr/local/bin/sprite-tunnel`. Writes the relay secret at `/home/sprite/.config/sprite-tunnel/relay.secret`, mode 0600.
4. Upserts service `tunnel` with `http_port: 8080`, then restarts it so a re-upload takes effect.
5. Applies an explicit `--url-auth`, checks health through ingress, and saves the URL locally.

Reinstalling intentionally disconnects the current client, which reconnects automatically. A different service already owning `http_port` causes installation to fail; it is not deleted. Setup expects a standard Sprite image with `sudo`, `install`, and `sprite-env`.

The service API supports environment variables, but sprites-go v0.2.0's `ServiceRequest` omits that field. We use a secret file instead. Its exec shutdown path also has a writer/deadline race reproduced by the race detector; four short setup commands use the documented exec protocol via coder/websocket. No shell interpolation or credentials in command arguments.

References: [Sprites API](https://sprites.dev/api), [services](https://sprites.dev/api/sprites/services), [exec protocol](https://sprites.dev/api/sprites/exec), [Go SDK](https://github.com/superfly/sprites-go).

## Protocol and behavior

```text
visitor → Sprite HTTPS ingress → relay HTTP reverse proxy
                                  ↓ yamux.Open
                            binary WebSocket
                                  ↓ yamux.Accept
                              laptop → local target
```

Relay is yamux client; laptop is yamux server. Both enable 15-second keepalives and a 1 MiB stream window. Every stream carries raw TCP bytes to the one configured local target. Go's ReverseProxy handles HTTP, streaming responses, and WebSocket upgrades. Each attachment owns its HTTP connection pool so a new client cannot reuse connections to the old one.

- No attached client: `503 tunnel not connected`.
- Local target unavailable: `502 Bad Gateway`; tunnel remains connected.
- Disconnection: jittered exponential retries, starting at 250–500 ms and capped at 30 s. Backoff resets after 30 stable seconds.
- Replacement: old WebSocket receives application close code **4001**, reason `replaced`; old client exits rather than competing to reconnect.
- Authentication rejection (401/403): exit without retry.
- Request cancellation: stream opens and local connections are canceled; shutdown closes hijacked sockets too.

Reserved routes are `GET /_tunnel` and `GET /_tunnel/health`. Health returns `{"connected":false,"since":null,"streams":0}` when detached. `streams` counts live yamux streams, including pooled HTTP connections. Access-log byte counts cover HTTP response bodies, not upgraded WebSocket payloads.

The default control credential is `Authorization: Bearer <tunnel-secret>`, compared using fixed-length SHA-256 digests and `subtle.ConstantTimeCompare`. For private ingress, `Authorization` carries the Sprite API credential and **`X-Tunnel-Authorization`** carries the tunnel bearer secret. The relay still checks the tunnel secret. This extension avoids two competing Authorization headers. Health is unauthenticated at the relay, but private Sprite ingress protects it along with every other route.

The relay does not expose local files, environment variables, or inspection endpoints. It forwards only through the attached client, which dials only `--to`.

## Verification

```sh
make check                 # go vet, pinned staticcheck, all race tests
make build
```

In-process tests cover GET, a multi-megabyte POST, 10 concurrent requests, WebSocket echo, health, 503, 502, sub-5-second reconnect, replacement, forwarding headers, shutdown, secure/idempotent secret creation, installation against a mock API, and URL-auth switching.

Opt-in live tests use an already-installed disposable Sprite:

```sh
export TEST_TUNNEL_URL=https://YOUR-ACTUAL-SPRITE-URL
export TEST_TUNNEL_SECRET_FILE=/path/to/tunnel.secret
# For a private URL:
export TEST_INGRESS_TOKEN_FILE=/path/to/sprites-api-token

go test -tags integration ./internal/tunnel -run TestLiveIngress -v

# This test deliberately switches the disposable Sprite public, then private:
export TEST_SPRITE_NAME=your-disposable-sprite
export TEST_API_TOKEN_FILE=/path/to/sprites-api-token
go test -tags integration ./cmd -run TestLiveCLIURLAuth -v
```

Production ingress was tested on 2026-09-09 with private and public URL auth: HTTP, POST, 10 concurrent requests, WebSocket echo, clean disconnect, and 35 idle seconds without reconnecting. Install and reinstall succeeded. The exact ingress idle-timeout limit remains undocumented/unconfirmed.

`internal/tunnel` has no CLI, terminal UI, or Sprites SDK dependency. It can be moved into the main Sprite CLI module when integrating as `sprite serve` (Go's `internal` import rule prevents importing it directly from an unrelated module).

## CI and release management

Pull requests and pushes to `main` run formatting, module consistency, vet, staticcheck, and race tests. Tagged releases rerun those checks, build six platform archives, create SHA-256 checksums, and publish through `gh`. Live tests are opt-in and are never run with repository secrets on pull requests. See [RELEASING.md](RELEASING.md).
