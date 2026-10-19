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

A protocol comes from one of two places: the machine-readable provider catalog (`models.opencode.ai/api.json`, which infers it from each model's SDK) and the **endpoint tables** in the upstream documentation. The tables are the **only place a System One route is declared** — such models inherit a provider-wide default SDK in the catalog, so a table that cannot be read leaves them without a protocol, advertises them as chat models, and the upstream then refuses them with `400 ModelProtocolUnsupported` (measured on `jev-1.13-free`: 400 on chat, responses and messages, and 200 on `/inference/v1/systemone`). The tables have moved once (`packages/web/src/content/docs/zen.mdx` became `services/www/src/docs/content/console/models.mdx`) and a 404 there is silent by nature; since `v1.3.5` an unreadable table is reported as `protocol_docs_unavailable` rather than looking like an upstream fault.

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

| Field                       | Default / requirement                                                                                           |
| --------------------------- | --------------------------------------------------------------------------------------------------------------- |
| `listen`                    | `127.0.0.1:8080`.                                                                                               |
| `server_keys`               | At least one local key is required.                                                                             |
| `zen_keys`, `go_keys`       | At least one upstream key is required unless anonymous access is enabled.                                       |
| `anonymous`                 | `false`.                                                                                                        |
| `prefer`                    | `go`; accepts `go` or `zen`.                                                                                    |
| `upstream.zen`              | `https://opencode.ai/inference`. A `/zen` value already saved is rewritten to it (that one host and path only). |
| `upstream.go`               | `https://opencode.ai/zen/go`.                                                                                   |
| `proxies`                   | Falls back to `["direct"]` when both proxy sources are empty.                                                   |
| `proxyfile`                 | Optional; relative paths resolve beside the configuration file.                                                 |
| `models.refresh_seconds`    | `300`; minimum 1.                                                                                               |
| `models.protocols`          | `{}`; per-model native protocol overrides.                                                                      |
| `models.strip_free_suffix`  | `false`; hide a trailing `-free` from advertised model IDs.                                                     |
| `reasoning.effort`          | Empty (off); `minimal`, `low`, `medium`, `high`, `xhigh`, `max`, or `none`.                                     |
| `reasoning.effort_by_model` | `{}`; per-model overrides of `reasoning.effort`.                                                                |

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
| `vless.subscription`            | Empty       | Subscription URL; must be http/https. Either this or `nodes` is required.                                             |
| `vless.nodes`                   | Empty       | Fixed `vless://` share links. May be combined with a subscription; fixed nodes take the first slots.                  |
| `vless.preferred_domains`       | Empty       | Entry domains (Cloudflare preferred domains or hand-picked IPs), see below.                                           |
| `vless.endpoints`               | Empty       | Service origins, handed out round-robin, see below.                                                                   |
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

`edge_ips` is the IP form of the same idea and the recommended way to scale a Cloudflare-fronted node: Cloudflare routes by SNI, so **any edge address that serves the zone is a valid entry**, and one link with N edge addresses becomes N independent tunnels (N listeners, one Xray process each). Only the address changes — UUID, SNI, host, path and transport stay exactly as the link declared. The expansion is ordered by edge first, so a `count` smaller than the full product still covers every worker before giving one of them a second listener. Up to 64 addresses, each a literal IP (a hostname is rejected outright rather than turning into a listener that can never connect). `tools/cf_ip_scan.py --apply` finds these addresses — including a 150-second long-connection hold test, because an edge that completes a TLS handshake is not necessarily one that survives an SSE stream — and writes them into `edge_ips`.

```json
"vless": {
  "enabled": true,
  "subscription": "",
  "nodes": ["vless://uuid@ct.example.com:443?security=tls&sni=edn2.example.com&type=ws&host=edn2.example.com&path=%2F"],
  "preferred_domains": [],
  "edge_ips": ["104.16.0.1", "104.19.0.1", "172.66.0.1", "188.114.96.1"],
  "endpoints": [],
  "count": 7
}
```

`endpoints` names several origins of the same service and hands them out **round-robin rather than at random**, because each usually carries its own request budget: an even spread spends the allowances together instead of letting random drift drain one early. Only the host and SNI change — the UUID identifies the user rather than the entry point, so it is unaffected. Leaving the list empty keeps every candidate on the host its link named.

Fixed-node candidates are ordered by measured latency, so the fastest take the earlier slots. Ranking runs in the background and never blocks startup, and a candidate that cannot be dialed sorts last instead of being dropped, which keeps the pool from shrinking below the configured `count`.

### IPv6 source address pool

When the provider routes an on-link IPv6 prefix (a `/56`, for example) to the host, **every address inside that prefix is a valid source for an outbound connection**, and the upstream sees each one as a different client. These exits need no proxy process, no tunnel handshake, and no third-party subscription, so they carry neither a provider request quota nor the failure modes of an expiring subscription or a node going dark.

```json
"ipv6_pool": {
  "enabled": true,
  "prefix": "2001:db8:1234:5600::/56",
  "interface": "eth0",
  "count": 64,
  "offset": 4096,
  "manage_addresses": true,
  "ready_timeout_seconds": 30
}
```

| Field                             | Default  | Meaning                                                                                                                                        |
| --------------------------------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| `ipv6_pool.enabled`               | `false`  | Inert while off, so direct / static proxies / vless are untouched.                                                                             |
| `ipv6_pool.prefix`                | Empty    | Required when enabled. The on-link prefix, `/64` or shorter.                                                                                   |
| `ipv6_pool.interface`             | `eth0`   | Interface the addresses belong to.                                                                                                             |
| `ipv6_pool.count`                 | `64`     | How many source addresses to keep, at most 1024. Each one becomes a transport.                                                                 |
| `ipv6_pool.offset`                | `4096`   | First host index used inside each `/64`, keeping clear of the host's address.                                                                  |
| `ipv6_pool.spread`                | `subnet` | How addresses are laid out across `/64` blocks. `subnet` puts one address in each (recommended); `address` packs them all into a single `/64`. |
| `ipv6_pool.manage_addresses`      | `true`   | Assign the addresses with `ip -6 addr add`; set false when provisioned elsewhere.                                                              |
| `ipv6_pool.ready_timeout_seconds` | `30`     | Budget for waiting out duplicate address detection.                                                                                            |

Behavior worth knowing:

- **Addresses are deterministic**: a restart reuses the same set instead of accumulating new addresses on the interface.
- **They must be spread across `/64` blocks**: the upstream meters the **`/64`**, not the individual address. `spread: subnet` (the default) distributes the pool over the blocks the prefix contains — a `/56` is 256 blocks, hence 256 separate allowances — while `spread: address` packs every address into one `/64`, which reads as a single client no matter how many addresses it holds. See the boundary note below.
- **They must be ready before any transport is built**: a freshly added address is `tentative` and binding it fails outright. The pool waits for each address to become usable and **drops only the ones that never do**, so a single slow address never costs the whole rotation.
- **A pool with no usable address fails the start** rather than silently falling back to `direct`, which would hide a misconfiguration behind traffic leaving from the wrong place.
- **Addresses are not removed on shutdown**: that avoids a restart race, and deterministic generation means nothing leaks.
- **Automatic assignment is Linux-only.** On other platforms set `manage_addresses: false` and provision the addresses outside the gateway.
- **The pool spreads across conversations, not across requests.** Exit selection carries session affinity, because the upstream rewards prompt-cache hits: **one conversation stays on one exit IP**, and only different conversations fan out over different addresses. A load test that repeats an identical body without a session header derives one conversation from the first user message and therefore uses a single exit — that is the intended behavior, not a pool that failed to engage. To observe the spread, vary `x-session-id` per request.

A single source address can also be written by hand in `proxies`, independently of the pool:

```
ipv6://2001:db8:1234:5600::1002
```

#### Choosing between this and the vless pool

|                  | vless pool                                                   | IPv6 source pool                          |
| ---------------- | ------------------------------------------------------------ | ----------------------------------------- |
| Exit location    | Remote real nodes                                            | Many source addresses in one local prefix |
| Depends on       | Subscription, node health, Xray processes                    | Only the interface                        |
| Rotation cost    | Rebuilding an Xray process (drain and truncated-stream risk) | Swapping a source address, stateless      |
| Request budget   | Bounded by the subscription provider                         | No third-party quota                      |
| Block resistance | Strong (different networks and ASNs)                         | Weak (one `/56`, one ASN)                 |

Both can run together. The transport list is assembled as static `proxies`, then the vless listeners, then the IPv6 source addresses.

> **Honest boundary (re-measured on 2026-10-15 against the tightened gate, so this is current)**: the upstream meters the **client address block** — not the individual address, and not the credential. Nothing a request can change moves that meter except the address it leaves from. The evidence, all of it taken while the pool was exhausted:
>
> - All 256 `/64` blocks of the `/56` (one address each, `spread: subnet`) were refused together, and `Retry-After` counted down to `00:00:00 UTC` exactly — a daily reset, measured twice, four seconds of countdown per four seconds of wall clock.
> - An address **inside a refused `/64` that the pool had never used** was refused on the same countdown, so the meter is not the address. It is a `/64` or wider; with every block already exhausted this run could not narrow it further — the earlier measurement below did, by repointing at a fresh `/64`.
> - Five different `x-opencode-project` values (`global`, a word, `prj_…`, hex, a random token) answered on the same countdown, so the project header is not the key.
> - A Cloudflare WARP egress holding the **same** `public` credential was served `200` for the same model, so the credential is not the key either.
>
> Earlier measurement (pre-2026-10-11 gate), which is also where the granularity comes from: with all 256 addresses packed into **one `/64`** and a 480-way burst, 444 of 480 came back `429`, and repointing the exit at a **`/64` that had never carried traffic** answered `200` immediately for the same model. So:
>
> - **`spread: subnet` is what makes this feature work.** One `/56` holds 256 `/64` blocks, hence 256 separate allowances; packing the addresses into one block is the same as using one client. **The distribution across `/64` blocks is the lever — the address count alone is not.**
> - **The allowance resets daily at UTC midnight**, and the total is smaller than the block count suggests. On 2026-10-15 the whole `/56` ran out somewhere between **67,000 and 73,100 served requests**: the first `429` arrived once 67,040 had been served, and the last success came 85 minutes later — the UTC day ended with **73,118 served** and **368,923 refused**. That is **~262–286 per `/64` per day** — a _count_, not a rate: it decides how much a day can carry, not how fast (at the peak 78/block/hour, a ~286 block-day is spent in a little over three hours). Well under the 1200–1500 per block measured earlier. The spread is the shape of the exhaustion rather than noise: blocks die one after another and the survivors absorb the load, so treat it as a band. Plan against the observed total rather than multiplying a per-block figure.
> - **The stability gain is certain**: 240 concurrent requests with zero failures and a steady 1.0–1.2 s p50, with no third-party subscription and no Xray process involved.
> - When one host's blocks are still not enough, add real `zen_keys` / `go_keys`, which are metered per account.

#### The free tier's client contract (where 403 comes from)

The anonymous free tier (`anonymous: true`) has a client-identity gate that is **completely independent of the exit IP**. When a request does not satisfy the contract, the upstream answers `403 FreeTierError` (`OpenCode's free tier can only be used from within OpenCode`) for **every free model on every exit**. Adding more source addresses or more `/64` blocks does nothing here — this is not throttling, it is the request identity being refused.

**One error message covers several unrelated causes**, so they have to be separated by ablation rather than guessed at. Four requirements are measured, plus one thing that must _not_ be sent:

| Requirement      | Meaning                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| ---------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Route            | Inference must go to `/inference/v1/*`. Upstream retired `/zen/v1/*`, which answers that same 403 for inference while **the catalog GET `/zen/v1/models` still returns 200** — which is exactly what makes it so misleading.                                                                                                                                                                                                                   |
| `stream: true`   | Chat requests must stream. The gateway forces this and folds the stream back into JSON for clients that asked for a plain reply.                                                                                                                                                                                                                                                                                                               |
| Tool definitions | The body must declare the core tools **with real content**, not just the right names: the description must not be empty and `parameters.properties` must not be empty. `bash` + `read` are the name minimum and five official definitions are enough. **One core tool whose definition does not look official is enough to have the whole request refused.** The gateway therefore declares those five from the embedded official definitions. |
| Session counter  | Both `x-opencode-session` and `x-opencode-session-id` must be sent, with their first 12 hex characters equal to the low 48 bits of `~(unixMillis*0x1000 + counter)` where **the counter must be small** (measured: 1 and 5 pass, 255 is refused).                                                                                                                                                                                              |
| Client headers   | `User-Agent`, `x-opencode-project` and `x-opencode-request` must each look like the real client's, and **all three must be right at once** (changing only one still answers 403): the UA needs a runtime token after `opencode/<version>` (a bare version is refused); the project is `global` (the gateway no longer synthesises `prj_…`); the request id must be `msg_` plus a canonically shaped identifier (no more `req_…`).              |
| ⛔ Must not send | **`x-session-affinity`**. A real client only emits that pair on non-opencode provider branches; on the inference route the header alone is enough to turn a 200 into a 403. `X-Session-Id` is tolerated but the real client does not send it either.                                                                                                                                                                                           |

The gateway applies all of these automatically. The gate was tightened twice:

- **2026-10-13**: it began decoding those 12 hex characters back into a counter and requiring a small one. Implementations that cut the field from a sha256 digest (including this gateway before `v1.3.0`) landed on a small counter only rarely, so every free model answered 403.
- **2026-10-14**: upstream renamed the anonymous inference route from `/zen` to `/inference`, made `x-session-affinity` fatal, and began judging the **content of the tool definitions**. **None of this is about session encoding**: replaying a genuine client's captured request byte-for-byte and changing only the route flips 403 to 200.

Measured boundary (2026-10-14, direct to the upstream, body and headers byte-identical, one variable at a time):

| Variable                                                                               | Result                                                                      |
| -------------------------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| `/zen/v1/chat/completions`                                                             | `403 FreeTierError`                                                         |
| `/inference/v1/chat/completions`                                                       | `200`, `text/event-stream`                                                  |
| Correct route + `x-session-affinity`                                                   | `403 FreeTierError`                                                         |
| Session's 36-bit timestamp field replaced by noise                                     | `200` (**the timestamp is not read**)                                       |
| Session mint time back-dated 48 hours                                                  | `200`                                                                       |
| Session counter 5 / 255                                                                | `200` / `403 FreeTierError`                                                 |
| Five official tool definitions                                                         | `200`                                                                       |
| Same five with `properties` emptied                                                    | `403 FreeTierError`                                                         |
| Same five with the descriptions emptied                                                | `403 FreeTierError`                                                         |
| Same five with `$schema` removed                                                       | `200` (not checked)                                                         |
| Same five with the **parameter** descriptions emptied                                  | `200` (not checked)                                                         |
| Same five with descriptions truncated to 300 chars                                     | `403 FreeTierError`                                                         |
| Same five with filler appended to the descriptions                                     | `200` (containment, not a hash)                                             |
| Eleven official tools, only `bash` made minimal                                        | `403 FreeTierError` (one impostor is enough)                                |
| Official definitions with the host-specific wording replaced                           | `200`                                                                       |
| UA `opencode/1.18.31 (linux amd64; go1.24.13)`                                         | `403 FreeTierError` (the only refused UA; it carried the Go runtime marker) |
| UA `opencode/1.19.0 (linux; x64)`, `opencode/1.19.0 linux`, `opencode/1.19.0 ai-sdk/…` | `200`                                                                       |
| `x-opencode-project` = `prj_<24 hex>`                                                  | `403 FreeTierError`                                                         |
| `x-opencode-project` = `global`, an arbitrary word, `my_project`, 32 hex               | `200`                                                                       |
| `x-opencode-request` = `req_<32 hex>`, `msg_`+anything, bare `msg_`                    | `403 FreeTierError`                                                         |
| `x-opencode-request` = `msg_` + 12 hex + 14 base62                                     | `200` (the encoding's content is not checked)                               |
| `Accept` = `application/json, text/event-stream`                                       | `200` (not checked)                                                         |
| UA / project / request with only one of them changed                                   | `403 FreeTierError` (all three must hold)                                   |

From `v1.3.2` the upstream base defaults to `https://opencode.ai/inference`, and **a `/zen` value left in an existing configuration is rewritten to `/inference`** — restricted to that one host and path, so a value naming a proxy or mirror is never touched. The gateway no longer sends `x-session-affinity` or `X-Session-Id`, only the header set the opencode provider itself uses. On the session side it validates the shape and the counter range; `encodeSessionPrefix` still writes a real mint millisecond exactly as the official algorithm does (quantised to the hour, so a conversation keeps one id for an hour), which keeps the encoded time obviously recent should upstream ever start reading it.

From `v1.3.3` the five core tools (`bash`, `edit`, `glob`, `grep`, `read`) are always declared from the **embedded official definitions**. A client's own tool of the same name is replaced rather than forwarded, because one definition that no longer looks official is enough to have the request refused; tools outside the core set (a client's `list_dir`, say) still pass through untouched. The trade-off is that the model sees the official signature for those five names, so the arguments it emits for them follow the official shape too.

From `v1.3.4` the `User-Agent` is the real client's string (`opencode/1.19.0 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.14`) instead of one assembled from the Go runtime — the old form, `opencode/1.18.31 (linux amd64; go1.24.13)`, was the only UA measured to be refused, and the `go1.…` marker is almost certainly why. `x-opencode-project` now defaults to `global` (a client's own value passes through) instead of a synthesised `prj_…`, and `x-opencode-request` is a `msg_` plus ascending identifier rather than `req_…`. All three are needed together: fixing any one alone still answers 403.

**A `403 FreeTierError` is a verdict on whoever owns the exit's source address.** The anonymous lane has exactly one identity (the shared `public` credential plus one client fingerprint), but that says nothing about who owns the address upstream saw:

- **The address is ours** (`ipv6://` pinned sources, `direct`): every such exit sits in one block, so rotating cannot change the verdict — scanning the pool only multiplies it. During the 2026-10-14 outage client latency grew from sub-second to the 12–184s range for a rejection that takes 0.5s to decide. The gateway **stops** on this response and records `anonymous_identity_rejected`.
- **The address is not ours** (`socks5://` / `http://` proxy exits, a Cloudflare Worker's egress for example): the refusal is that egress's own answer, and the next listener may well succeed. From `v1.3.8` such an exit **keeps rotating** (`anonymous_exit_refused`, debug level) and the refusal is _not_ recorded lane-wide — recording it would let one bad egress refuse traffic the rest of the pool could have served.

The gateway classifies this from the exit name, so nothing has to be configured.

**A `429` is a verdict on the lane too, and is now treated like one.** It is an allowance judgement on the client address block (see the boundary note above), so no exit can escape it. From `v1.3.6` the lane **keeps the last lane-level refusal** — a `429`, or the `403 FreeTierError` identity verdict — and once every exit is cooling it answers later requests from that record: same status, same message, `Retry-After` counting down to the same deadline, without touching an upstream that has already spoken. It logs `anonymous_lane_refused` when the window is learned and `anonymous_refusal_replayed` for each request answered from it. The record is dropped as soon as a request is served again, and expires exactly when upstream said it would, so recovery needs no intervention. A successful catalog refresh deliberately does **not** drop it: that route answers `200` even while inference is rate-limited (the same trap that made a retired inference path look alive), so clearing on it would send the next request back to probing. Before this, an exhausted lane still fired one probe per request for the entire window — on 2026-10-15 that was 378,700-odd requests over six and a half hours, each one still reaching an endpoint that had asked the caller to wait fifty-five minutes.

**Read the status code first**: `403 FreeTierError` is a contract problem (upgrade to the latest release first); `429` is a budget problem (see "429 and the daily allowance" in DEPLOYMENT.md).

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
| `config.json` edits seem ignored     | Check validation errors in the event log; the previous config keeps running.              |
| WebUI unreachable over the LAN       | `webui.listen` must be `0.0.0.0` rather than `127.0.0.1`.                                 |
| Monitoring disappeared after restart | Monitoring is stored only in memory; collect stdout logs externally.                      |

## Acknowledgements

Thanks to the [LINUX DO](https://linux.do) community for its support.
