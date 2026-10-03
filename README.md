# opencode2api

[English](README.md) | [简体中文](README.zh-CN.md)

A Go gateway for **OpenCode Zen and Zen Go**. It exposes OpenAI Chat Completions, Responses, and Anthropic Messages endpoints, translates between the upstream's native protocols, and manages upstream keys and proxy pools in one place.

The management console is embedded in the executable, so running the service requires **neither Node.js nor a database**.

> **Deployment guide**: four deployment methods, first-time configuration, verification steps, troubleshooting, and hardening are covered in **[DEPLOYMENT.md](DEPLOYMENT.md)**.

## Features

- Bidirectional conversion of both plain JSON and SSE streams across the three inference protocols.
- Text, images, reasoning, function tools, tool calls, and tool results; file content wherever the target protocol can represent it.
- A dedicated `/v1/systemone` endpoint for OpenCode's decision models, forwarded verbatim without shape conversion.
- Separate Zen and Go key pools with configurable preference, retries, and session affinity.
- Optional anonymous access to eligible free models using the OpenCode `public` credential.
- Direct, HTTP, HTTPS, SOCKS5, and SOCKS5H connections, including a proxy file.
- A vless subscription proxy pool that accepts both v2ray and Clash / mihomo subscriptions, giving every node its own Xray process and egress IP, rotated in batches. **Xray is downloaded and verified automatically when missing**, so no manual setup is needed.
- Dynamic model discovery, native protocol detection, and on-disk caches.
- A separate management port with an overview, routing diagnostics, configuration editing, and a live event log.
- Configuration hot reload: a replacement gateway is validated and built before new requests are switched over, and a failure keeps the original instance.

## Quick start

Download a binary from [GitHub Releases](https://github.com/wu-jiyan/opencode2api/releases), or build with **Go 1.24 or newer**:

```bash
git clone https://github.com/wu-jiyan/opencode2api.git
cd opencode2api
cp config.example.json config.json
go build -o opencode2api ./cmd/opencode2api
```

Edit `config.json` before starting:

1. Replace `server_keys` with your own local API key.
2. Supply Zen or Go keys. Alternatively, set `anonymous: true` and leave both upstream key arrays empty.
3. Replace `webui.password`. The example enables the WebUI with username `admin`.

```bash
./opencode2api -config config.json
```

On Windows, copy the config with `Copy-Item config.example.json config.json`, build with `go build -o opencode2api.exe ./cmd/opencode2api`, and run `.\opencode2api.exe -config config.json`.

The example listens on `127.0.0.1:8080` for the API and `0.0.0.0:8081` for the WebUI. Open `http://localhost:8081` locally. **When reaching the console over a network, restrict access and put it behind an HTTPS reverse proxy** — the console has a single shared administrator account and its login throttling is kept in memory rather than persisted.

| Option        | Default       | Purpose                            |
| ------------- | ------------- | ---------------------------------- |
| `-config`     | `config.json` | Configuration file path.           |
| `-listen`     | Unset         | Override the API listen address.   |
| `-web-listen` | Unset         | Override the WebUI listen address. |

The configuration directory must be writable for password migration, configuration saves, and model caches.

## Binary

Download the archive for your platform from [Releases](https://github.com/Wu-jiyan/opencode2api/releases). Linux and macOS ship `.tar.gz`, Windows ships `.zip`, and every archive comes with a `.sha256` file — verify it before use:

```bash
VERSION=v1.0.0
ARCH=linux_amd64        # or: linux_arm64, darwin_arm64, darwin_amd64
curl -LO "https://github.com/Wu-jiyan/opencode2api/releases/download/${VERSION}/opencode2api_${VERSION}_${ARCH}.tar.gz"
curl -LO "https://github.com/Wu-jiyan/opencode2api/releases/download/${VERSION}/opencode2api_${VERSION}_${ARCH}.tar.gz.sha256"
sha256sum -c "opencode2api_${VERSION}_${ARCH}.tar.gz.sha256"
tar -xzf "opencode2api_${VERSION}_${ARCH}.tar.gz"
cd "opencode2api_${VERSION}_${ARCH}"
vi config.json
./opencode2api -config config.json
```

On macOS use `shasum -a 256 -c` instead of `sha256sum -c`. On Windows, download the `.zip`, compare it with `Get-FileHash -Algorithm SHA256`, and extract it with `Expand-Archive`.

Each archive contains the binary for that platform, a `config.json` pre-filled with placeholders, and the three documentation files. **No launcher script is bundled** — a batch file would need a specific code page to stay readable — so run the binary directly. If vless is enabled and Xray is missing locally, the service downloads it in the background.

See [DEPLOYMENT.md](DEPLOYMENT.md) for the full guide, including systemd units, per-platform commands, and how configuration changes take effect.

## API usage

`server_keys` authenticate clients to this gateway. They are separate from `zen_keys` and `go_keys` and are never used as upstream credentials.

Send `Authorization: Bearer YOUR_LOCAL_API_KEY` or `x-api-key: YOUR_LOCAL_API_KEY`. Health checks require no authentication.

| Method | Path                   | Description                                               |
| ------ | ---------------------- | --------------------------------------------------------- |
| GET    | `/v1/models`           | Models that can be routed with the current configuration. |
| POST   | `/v1/chat/completions` | Chat Completions.                                         |
| POST   | `/v1/responses`        | Responses.                                                |
| POST   | `/v1/messages`         | Anthropic Messages.                                       |
| POST   | `/v1/systemone`        | Decision models. Relayed verbatim, not converted.         |
| GET    | `/healthz`             | Readiness and resource summary.                           |

Discover an available model first:

```bash
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer YOUR_LOCAL_API_KEY"
```

Replace `MODEL_ID` below with an ID from that response.

**Chat Completions**

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer YOUR_LOCAL_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"MODEL_ID","messages":[{"role":"user","content":"Hello"}]}'
```

**Responses**

```bash
curl http://localhost:8080/v1/responses \
  -H "Authorization: Bearer YOUR_LOCAL_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"MODEL_ID","input":"Hello"}'
```

**Anthropic Messages**

```bash
curl http://localhost:8080/v1/messages \
  -H "x-api-key: YOUR_LOCAL_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"MODEL_ID","max_tokens":512,"messages":[{"role":"user","content":"Hello"}]}'
```

For streaming, add `"stream": true` to the body and use `curl -N`. Responses carry an `x-request-id` header for correlation with the logs. API request bodies are limited to 32 MiB.

### Compatibility boundaries

When the client protocol matches the upstream's native protocol, provider-specific fields are preserved. Cross-protocol requests pass through a common representation, and not every option has an equivalent: for example, Chat JSON output constraints and `seed` are not forwarded to Anthropic Messages. Unsupported content or tool types may be rejected.

The gateway exposes the routes listed above. It does not implement embeddings, file uploads, image generation, or Responses retrieval and cancellation. It does not store conversation history; clients must supply the history or use features supported by the actual upstream.

## Routing and models

### Model discovery

The gateway refreshes Zen/Go `/v1/models` and OpenCode's [capability catalog](https://models.opencode.ai/api.json) on the configured interval, tracking native protocols and model limits separately for each tier. When a protocol cannot be determined, OpenCode's Zen/Go documentation is used as a fallback.

`models.protocols` overrides discovery:

```json
{
  "models": {
    "refresh_seconds": 300,
    "protocols": {
      "custom-model": "chat"
    }
  }
}
```

Allowed protocol values are `chat`, `responses`, `anthropic`, and `systemone`. Models using an unsupported native protocol are filtered from discovery unless overridden.

`models.strip_free_suffix` hides a trailing `-free` from the advertised model IDs: `/v1/models` then returns `deepseek-v4-flash` instead of `deepseek-v4-flash-free`. Both spellings keep working for inference — an exact catalog match wins, and only an unmatched ID falls back to `name + "-free"` — so enabling or disabling it never breaks an existing client. The upstream always receives the real ID. A variant whose stripped name would collide with an existing catalog entry is left alone instead of shadowing it. The switch only changes the advertised name; the `model` echoed in a response body stays the upstream's real ID.

Cost and deprecation metadata come from [models.dev](https://models.dev/api.json), refreshed every 24 hours with a 30-second timeout per HTTP client. Refresh failures retain existing data.

### Anonymous access and fallback

With `anonymous: true`, either condition makes a model eligible for the anonymous Zen lane:

- Its ID contains `free`, case-insensitively.
- models.dev reports zero input and output cost and the model is not deprecated.

Eligibility is a routing decision; the upstream can still reject or rate-limit the request. Anonymous requests use `public` in the upstream authentication header.

The routing sequence is:

1. For an eligible model, try each available anonymous proxy once.
2. Try authenticated tiers in `prefer` order, using only tiers with a configured key and a route for that model.
3. Apply `retry.max_attempts` separately to each authenticated tier.

Anonymous attempts are not cut short by `retry.max_attempts`, but all attempts share the request timeout. Network errors, authentication failures, rate limits, and server errors can rotate keys. Other 4xx responses end the current tier; another available tier may still be tried.

Requests are encoded for each tier's own protocol. Once a stream has started, the gateway does not retry generation on another node. A recognized stale Responses reasoning reference can trigger one repair pass; selected-key diagnostics never use that replay.

When only anonymous access is configured, `/v1/models` exposes only models eligible for that lane.

### Sessions and proxies

Keys are initially spread across proxies. Real traffic can trigger proxy checks, key rebinding, and cooldowns. Unhealthy proxies are rechecked every 15 minutes against Cloudflare trace.

A stable session hash selects the preferred key or anonymous proxy. Send an explicit `x-session-id` to separate independent conversations. The gateway also accepts `x-opencode-session`, `x-session-affinity`, `conversation-id`, `conversation_id`, and `metadata.session_id`.

Without an explicit ID, the first user message is used as a seed. Conversations beginning with the same content can therefore share affinity. Changing pool membership can change the selected node.

Cooldowns grow exponentially up to eight times `performance.failure_cooldown_seconds`, and a longer `Retry-After` is honored. If every authenticated key is cooling down, routing may try the key whose cooldown ends first. Anonymous nodes still in cooldown are skipped.

## Configuration reference

See [config.example.json](config.example.json) for a complete starting configuration. JSON supports `//` and `/* ... */` comments; unknown fields and invalid values are rejected.

### Keys, listeners, and routing

| Field                       | Default / requirement                                                       |
| --------------------------- | --------------------------------------------------------------------------- |
| `listen`                    | `127.0.0.1:8080`.                                                           |
| `server_keys`               | At least one local key is required.                                         |
| `zen_keys`, `go_keys`       | At least one upstream key is required unless anonymous access is enabled.   |
| `anonymous`                 | `false`.                                                                    |
| `prefer`                    | `go`; accepts `go` or `zen`.                                                |
| `upstream.zen`              | `https://opencode.ai/zen`.                                                  |
| `upstream.go`               | `https://opencode.ai/zen/go`.                                               |
| `proxies`                   | Falls back to `["direct"]` when both proxy sources are empty.               |
| `proxyfile`                 | Optional; relative paths resolve beside the configuration file.             |
| `models.refresh_seconds`    | `300`; minimum 1.                                                           |
| `models.protocols`          | `{}`; per-model native protocol overrides.                                  |
| `models.strip_free_suffix`  | `false`; hide a trailing `-free` from advertised model IDs.                 |
| `reasoning.effort`          | Empty (off); `minimal`, `low`, `medium`, `high`, `xhigh`, `max`, or `none`. |
| `reasoning.effort_by_model` | `{}`; per-model overrides of `reasoning.effort`.                            |

### Forced thinking level

`reasoning.effort` sets the thinking level used upstream when a request does not state one of its own, and `reasoning.effort_by_model` overrides it for the model IDs it names. It applies to all three protocols, in either direction of conversion, and matters most for cross-protocol traffic: an Anthropic `thinking.budget_tokens` can only be approximated by the Chat and Responses surfaces, so a forced level reaches a rung that the budget alone cannot express.

```json
{
  "reasoning": {
    "effort": "high",
    "effort_by_model": { "claude-opus-5": "max" }
  }
}
```

A level a client states explicitly always wins, so this only replaces a level nobody asked for: an absent one, or one derived from `thinking.budget_tokens`. Use `"none"` to remove reasoning from requests that do not ask for it.

Levels are reported faithfully in both directions. An explicit `output_config.effort` survives conversion instead of being shadowed by a `thinking` block, `budget_tokens` keeps its rung (`8192` is `high`, `32000` is `xhigh`, so the two stay distinguishable), and the Responses target carries the level as `reasoning.effort` rather than dropping it.

The WebUI Configuration Center exposes the same default and per-model fields. The management API returns them as `reasoning` from `GET /api/config` and accepts the object in `PUT /api/config`; clients that omit the field keep the existing value, while sending an empty object clears it.

Proxy entries accept `direct`, `http://`, `https://`, `socks5://`, and `socks5h://`, including URL credentials. The inline list is merged with `proxyfile` and deduplicated in order.

A proxy file contains one entry per line and supports blank lines and comments:

```text
# Primary proxy
http://user:password@127.0.0.1:7890
socks5://127.0.0.1:1080  # Backup proxy
direct
```

Comment markers are `#`, `;`, and `//` at the start of a line or after whitespace.

### vless proxy pool

The `vless` section turns one upstream subscription into a set of local SOCKS5 listeners. Each listener is served by its own Xray process and therefore has its own egress IP. When enabled, these local listeners are appended after the static proxies and take part in pool rotation.

Subscriptions are accepted in both **v2ray** (base64 or a plaintext `vless://` list) and **Clash / mihomo** (YAML) formats.

| Field                           | Default     | Meaning                                                                                                               |
| ------------------------------- | ----------- | --------------------------------------------------------------------------------------------------------------------- |
| `vless.enabled`                 | `false`     | Turn on the vless pool.                                                                                               |
| `vless.subscription`            | Empty       | Subscription URL; must be http/https. Either this or `nodes` is required.                                                             |
| `vless.nodes`                   | Empty       | Fixed `vless://` share links. May be combined with a subscription; fixed nodes take the first slots.                              |
| `vless.preferred_domains`       | Empty       | Entry domains (Cloudflare preferred domains or hand-picked IPs), see below.                                                          |
| `vless.endpoints`               | Empty       | Service origins, handed out round-robin, see below.                                                                                 |
| `vless.count`                   | `24`        | Number of local listeners kept alive.                                                                                 |
| `vless.refresh_seconds`         | `300`       | Base interval for rolling rebuilds.                                                                                   |
| `vless.rotate_batch`            | `3`         | Listeners rebuilt per round; smaller keeps the pool warmer, larger rotates it faster.                                 |
| `vless.xray_path`               | Empty       | Xray executable. Relative paths resolve beside the config directory; empty looks for `bin/xray/xray[.exe]` or `PATH`. |
| `vless.auto_download_xray`      | `true`      | Download the official release automatically when no Xray is found.                                                    |
| `vless.xray_version`            | Empty       | Pin the downloaded version, for example `v26.3.27`; empty means the latest release.                                   |
| `vless.port_base`               | `24000`     | First listener port; listener _i_ uses `port_base + i`.                                                               |
| `vless.host`                    | `127.0.0.1` | Local listen address.                                                                                                 |
| `vless.subscription_proxy`      | Empty       | Optional proxy used to fetch the subscription itself.                                                                 |
| `vless.startup_timeout_seconds` | `15`        | How long to wait for one Xray instance to start listening.                                                            |

Rotation strategy: when `refresh_seconds` elapses, a batch of `rotate_batch` listeners is rebuilt, so the pool is never interrupted as a whole. Each new node is verified on a temporary port before it replaces an old process, which means "an IP change after rebuilding one node" never costs a failed connection.

#### Fixed nodes, entry domains, and service origins

Besides a subscription you can configure fixed `vless://` links directly. That suits Cloudflare-fronted nodes: the pool keeps working when a subscription URL goes away, and it lets you pin exactly the routes you want.

`preferred_domains` replaces a fixed node's **entry address** with every value in the list, producing one candidate each while keeping the UUID, SNI, host, path, and transport untouched. A single link can therefore fill several independent egress routes. A node whose address is a literal IP and appears in no list is used as-is and never rewritten.

```json
"vless": {
  "enabled": true,
  "subscription": "",
  "nodes": ["vless://uuid@ct.example.com:443?security=tls&sni=edn2.example.com&type=ws&host=edn2.example.com&path=%2F"],
  "preferred_domains": ["cf.example.com", "ct.example.com", "cu.example.com"],
  "endpoints": ["edn2.example.com", "edn3.example.com", "edn4.example.com"],
  "count": 7
}
```

`endpoints` names several origins of the same service and hands them out **round-robin rather than at random**, because each usually carries its own request budget: an even spread spends the allowances together instead of letting random drift drain one early. Only the host and SNI change — the UUID identifies the user rather than the entry point, so it is unaffected. Leaving the list empty keeps every candidate on the host its link named.

Fixed-node candidates are ordered by measured latency, so the fastest take the earlier slots. Ranking runs in the background and never blocks startup, and a candidate that cannot be dialed sorts last instead of being dropped, which keeps the pool from shrinking below the configured `count`.

### Automatic Xray download

With `vless.enabled` on and no Xray executable found, the gateway downloads the official release **in the background** (startup is never blocked) into `bin/xray/` under the configuration directory, and reuses it on restart. The sequence is:

1. Derive the official release asset name from the platform and architecture (for example `Xray-linux-64.zip`).
2. Query the latest release tag when `xray_version` is empty.
3. Try each mirror in turn, fetching both the archive and its `.dgst` digest file.
4. **Verify the SHA-256**; on mismatch fall through to the next mirror and never write the file.
5. Write to a `.download` staging name and rename atomically, extracting only `xray` / `xray.exe` (skipping roughly 30 MiB of geo data).

Mirrors are ordered by **measured sustained throughput**, not ping latency — a host that answers a HEAD request quickly can still be far too slow for the payload:

| Order | Mirror                | Measured throughput               |
| ----- | --------------------- | --------------------------------- |
| 1     | `gh-proxy.com`        | ~0.5 MiB/s                        |
| 2     | `ghfast.top`          | ~0.09 MiB/s                       |
| 3     | `ghproxy.net`         | ~0.01 MiB/s                       |
| 4     | `github.com` (direct) | frequently times out; last resort |

Progress appears in the event log: `xray installed` on success, `automatic xray download failed` when every mirror failed. In that case, place the binary manually and point `vless.xray_path` at it, or set `auto_download_xray` to `false` to forbid network access entirely.

Linux, Windows, and macOS are supported on amd64, arm64, 386, and arm (v7).

### Timeouts and connection pools

| Field                                     | Default | Meaning                                                |
| ----------------------------------------- | ------- | ------------------------------------------------------ |
| `retry.max_attempts`                      | `3`     | Attempts per authenticated tier, including the first.  |
| `retry.timeout_seconds`                   | `300`   | Total inference timeout, including stream consumption. |
| `performance.attempt_timeout_seconds`     | `0`     | Header wait per attempt; 0 uses the request timeout.   |
| `performance.connect_timeout_seconds`     | `5`     | Connection establishment timeout.                      |
| `performance.failure_cooldown_seconds`    | `15`    | Base failure cooldown.                                 |
| `performance.max_idle_conns`              | `2048`  | Idle connection limit per proxy transport.             |
| `performance.max_idle_conns_per_host`     | `256`   | Idle connection limit per host and transport.          |
| `performance.max_conns_per_host`          | `0`     | Connection limit per host; 0 is unlimited.             |
| `performance.idle_conn_timeout_seconds`   | `120`   | Idle connection lifetime.                              |
| `performance.connection_rotation_seconds` | `0`     | Idle connection recycling interval; 0 disables it.     |

The per-attempt header timeout is capped by the request timeout. Set it below the total timeout if a slow upstream should leave time for fallback. Expired request contexts stop further attempts without penalizing unused keys or proxies.

A vless entry hands every new connection a different egress IP, so with `performance.connection_rotation_seconds > 0` the gateway recycles idle connections and the next request dials again to rotate its outbound IP. Recycling is staggered across listeners (one per tick) so the whole pool never pays the handshake at once, and requests arriving back to back still reuse a warm connection.

Cost reference: a fresh tunnel connection costs roughly 0.6–1.3 s more than a reused one (higher for trace-style targets). Set the value well above the typical request gap — `300`, matching `vless.refresh_seconds`, is a reasonable choice. `0` disables rotation entirely.

### Logging and management

| Field                         | Default / requirement                                         |
| ----------------------------- | ------------------------------------------------------------- |
| `logging.level`               | `info`; accepts `debug`, `info`, `warn`, `error`.             |
| `logging.ring_size`           | `2000`; range 100–50,000.                                     |
| `logging.dump_request_bodies` | `false`. Requires `debug` logging to emit outbound bodies.    |
| `webui.enabled`               | `false` when omitted; the example sets it to `true`.          |
| `webui.listen`                | `0.0.0.0:8081`.                                               |
| `webui.username`              | Required when the WebUI is enabled; the example uses `admin`. |
| `webui.password`              | Bootstrap password with a minimum length of 10.               |
| `webui.password_hash`         | Generated Argon2id hash; replaces the plaintext password.     |
| `webui.session_ttl_minutes`   | `720`; range 5–10,080.                                        |

On startup, a bootstrap password is hashed and removed from the configuration. The backup containing that bootstrap plaintext is deleted. Keep configuration files and backups private: upstream keys and proxy credentials remain necessary configuration secrets.

Body dumps are opt-in and capped at 64 KiB per prepared body. Configured secrets are redacted, but conversation content may still be sensitive.

## WebUI and diagnostics

The management listener serves the UI and `/api/*` routes. Authentication uses one administrator account, server-side sessions, HttpOnly/SameSite cookies, CSRF checks for writes, and login throttling.

This gateway is meant to be consumed as an upstream channel by an aggregation panel such as new-api. Per-request usage, token totals, request timelines, and channel testing belong to that panel, so the console keeps only what is unique to this process: upstream key and proxy-pool management, protocol routing diagnostics, and in-process event logs. The console has four pages: **Overview**, **Routing diagnostics**, **Configuration**, and **Event log**.

| Management route                                 | Purpose                                                                      |
| ------------------------------------------------ | ---------------------------------------------------------------------------- |
| `POST /api/auth/login`                           | Log in and obtain a session cookie and CSRF token.                           |
| `GET /api/auth/session`, `POST /api/auth/logout` | Inspect or end the session.                                                  |
| `GET /api/config`, `PUT /api/config`             | Read masked configuration or apply changes.                                  |
| `POST /api/config/reload`                        | Reload the file from disk.                                                   |
| `GET /api/monitor`                               | In-process request counts, success rate, latency percentiles, and resources. |
| `GET /api/debug/models`                          | Model routes, anonymous eligibility, and pricing metadata.                   |
| `POST /api/nodes/probe`                          | Time to first byte per proxy node (read-only; health is untouched).          |
| `POST /api/models/refresh`                       | Pull the model catalog and models.dev pricing metadata immediately.          |
| `GET /api/logs`, `GET /api/logs/stream`          | Recent logs or a live SSE subscription.                                      |

### Saving and reloading

The server validates a candidate configuration and builds its replacement gateway before saving and switching. Failed validation or persistence leaves the active gateway in place. Requests already running continue on their original gateway.

Keys, proxies, upstream URLs, retry settings, model settings, logging, and routing preferences apply to new requests immediately. Changes to `listen`, `webui.listen`, or `webui.enabled` require a process restart. Saved JSON is normalized; comments are not retained.

## Monitoring and persistence

Request outcomes describe the completed inference. A failed SSE response or truncated stream counts as an error even when HTTP 200 was already sent; its outcome is `stream_error`. Client cancellations are recorded as `client_canceled`.

In-process monitoring aggregates only request counts, success rate, and latency percentiles (a rolling 60-minute window plus lifetime totals). Token usage and request/attempt timelines are not aggregated here: the aggregation panel reads the `usage` object from each response, and this gateway only relays upstream responses unchanged.

| Data                               | Storage / retention                                     |
| ---------------------------------- | ------------------------------------------------------- |
| Sessions and metrics               | Process memory; cleared on restart.                     |
| Live log ring                      | Process memory; size set by `logging.ring_size`.        |
| Structured logs                    | JSON on stdout; collect externally for durable history. |
| Configuration and previous version | `config.json` and `config.json.bak`.                    |
| Model directory cache              | `config.json.models.catalog.json`.                      |
| Price/deprecation cache            | `config.json.models.dev.json`.                          |

Cache filenames are based on the actual configuration path. Lifetime counters mean the current process lifetime. Instances do not share sessions or monitoring state.

### Health checks

`/healthz` does not increment monitoring counters or trigger network requests. It reports resource counts without keys or proxy addresses.

- HTTP 503: initial catalog pending, no models routable with the current configuration, or no healthy proxies.
- HTTP 200: ready, including when a usable catalog cache is stale. In that case the model status is `stale` and the overall status is `degraded`.

The staleness threshold is twice `models.refresh_seconds`, with a minimum of 60 seconds. Readiness is a discovery and resource check; it does not verify that an upstream key will accept the next inference.

## Development

Go source is organized into packages by responsibility. The command wires the service together; implementation packages live under `internal/`.

```text
cmd/
  opencode2api/main.go    CLI flags, startup, and graceful shutdown
internal/
  admin/                 Management API, login sessions, and diagnostics
  buildinfo/             Version shared by health and management endpoints
  config/                Configuration, persistence, passwords, and redaction
  gateway/               HTTP routing, retries, pools, refresh, and runtime
  httpx/                 Shared HTTP responses, body handling, and headers
  identity/              Request IDs and session affinity
  jsonutil/              JSON access and decoding helpers
  models/                Model catalog, capabilities, pricing, and caches
  protocol/              Request/response conversion and SSE parsing/output
  telemetry/             Request tracking, metrics, logs, and recovery
  vless/                 Subscription parsing and the Xray process pool
webui/
  embed.go               Embeds the built console (dist) into the executable
  index.html             Console entry (Vite source)
  src/                   Vue 3 sources: entry, API client, views, components
  dist/                  Vite build output, committed and embedded
```

The Vite configuration lives at the repository root in `vite.config.js`.

Suggested reading order:

1. [Command entry](cmd/opencode2api/main.go) → [runtime management](internal/gateway/runtime.go) → [HTTP handlers](internal/gateway/gateway.go).
2. [Upstream attempts](internal/gateway/upstream.go) and [model routing](internal/models/catalog.go) explain how a request reaches a provider.
3. [Request conversion](internal/protocol/request.go), [response conversion](internal/protocol/response.go), and [stream transport](internal/protocol/stream.go) cover protocol behavior.
4. [Management routes](internal/admin/server.go) and [WebUI source](webui/src/App.vue) cover configuration and diagnostics.

Run Go formatting, analysis, and the command build:

```bash
gofmt -w cmd internal webui
go vet ./...
go build -o opencode2api ./cmd/opencode2api
```

For local development, start the service with `go run ./cmd/opencode2api -config config.json`. Release builds still inject the version with `-ldflags "-X main.version=vX.Y.Z"`.

Node.js is needed to build the console and to format sources. `webui/dist` is committed, so building the gateway binary itself does not require Node.js; rebuild the console after changing front-end sources:

```bash
npm ci --ignore-scripts
npm run build     # regenerates webui/dist
npm run format
npm run format:check
```

`.editorconfig`, `.gitattributes`, Go formatting, and pinned Prettier settings standardize the source. CI runs `go vet` and builds with Go 1.24 and stable Go on Linux/Windows, plus formatting checks, a WebUI build, and entrypoint shell syntax checks. Release archives include both READMEs.

## Troubleshooting

| Symptom                              | Check                                                                                     |
| ------------------------------------ | ----------------------------------------------------------------------------------------- |
| API returns 401                      | Use a configured local `server_keys` value.                                               |
| Health remains `starting`            | Inspect catalog refresh logs and outbound connectivity; add protocol overrides if needed. |
| No models are exposed                | Check configured tiers, anonymous eligibility, and native protocol support.               |
| Requests return 502/504              | Inspect upstream attempts, credentials, proxies, and total/per-attempt timeouts.          |
| HTTP 200 but generation failed       | Inspect the SSE error event and request outcome, not only HTTP status.                    |
| `config.json` edits seem ignored      | Check validation errors in the event log; the previous config keeps running.               |
| WebUI unreachable over the LAN        | `webui.listen` must be `0.0.0.0` rather than `127.0.0.1`.                                 |
| Monitoring disappeared after restart | Monitoring is stored only in memory; collect stdout logs externally.                      |

## Acknowledgements

Thanks to the [LINUX DO](https://linux.do) community for its support.
