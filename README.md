# Radar — Ephemeral P2P Group Location

A **stateless, serverless-in-spirit** live location radar. A group opens a shared
link, and every device plots every other device on a map in real time — GPS
coordinates, speed and heading flowing **peer-to-peer over WebRTC DataChannels**,
never through a database. When the last tab closes, the group and its route
history simply cease to exist.

Built for the moments you want to see your people move — a convoy through Pune
traffic, friends spread across a festival, a group ride — without leaving a
permanent trail on someone's servers.

## Key principles

- **No location on any server.** The Go service is a *signaling relay* only. It
  brokers the WebRTC handshake and nothing else — coordinates never touch it.
- **Zero persistence.** All state is in-memory. A room is forgotten the instant
  its last member disconnects.
- **Opt-in by design.** You only ever join a radar through an explicit shared
  code. There is no "discover people near you" — sharing live GPS is far too
  sensitive for implicit grouping.
- **Honest degradation.** Stale markers fade and show "last seen 12s ago";
  unreachable peers say so. The app never renders a confident wrong position.

> **On "serverless":** true peer-to-peer still needs a tiny signaling step to
> introduce browsers to each other, so one minimal relay exists. It stores
> nothing and sees no coordinates. That's the honest framing — *stateless
> servers*, not *no servers*.

## Architecture

```
 Phone A ─┐        ┌───────────────┐        ┌─ Phone B
 Phone B ─┼──WS──► │  Signaling    │ ◄──WS──┼─ Phone C
 Phone C ─┘        │  relay (Go)   │        └─ …
                   └───────────────┘
   │  Once introduced, every pair opens a direct WebRTC DataChannel:
   └────────── full mesh, GPS flows P2P, server no longer involved ──────────┘
```

- **Signaling** (this repo, `*.go`): a WebSocket hub that groups clients into
  rooms and routes SDP offers/answers and ICE candidates between peers in the
  same room. Deployed to **Render** as a `scratch` Docker image.
- **Client** (`frontend/index.html`): builds a **full WebRTC mesh** — every peer
  connects to every other peer — and broadcasts its position on each channel.
  Rendered with **MapLibre GL JS**. Deployed to **Cloudflare Pages**.

### Design decisions

| Decision | Choice | Why |
| :-- | :-- | :-- |
| Group size | **Capped at 8** | Mesh connections grow as O(n²). Eight keeps bandwidth and battery sane. Enforced server-side (503 on the 9th join). |
| NAT traversal | **STUN only, no TURN** | No relayed traffic to run or disclose. Trade-off: some peer pairs on symmetric NAT / CGNAT won't connect — the UI reports that honestly instead of hiding it. |
| Room secret | **URL fragment** (`#room=CODE`) | The fragment isn't sent in the initial page request, so the room code stays off server logs. The client reads it and passes it to the WS relay for routing. |
| Position transport | **Unreliable, unordered DataChannel** | Latest position wins; a dropped 2s-old packet is worthless, so retransmits are disabled for lower latency. |

### Mesh formation (no glare)

For any pair of peers, the one with the **lexicographically smaller id** creates
the offer; the other waits. This single rule governs both first contact and
reconnection, so two peers never simultaneously offer to each other. On a failed
connection the initiator retries with backoff (up to 6 attempts) before marking
the peer unreachable.

## Signaling protocol (JSON over WebSocket)

| Type | Direction | Description |
| :-- | :-- | :-- |
| `welcome` | S → C | Assigned `id`, `name`, and `room` on connect. |
| `peers` | S → C | Existing peers in the room (sent once on join). |
| `peer-joined` | S → all | A new peer joined the room. |
| `peer-left` | S → all | A peer disconnected. |
| `offer` / `answer` / `ice-candidate` | C ⇄ C | WebRTC handshake, routed to a specific `to` peer in the same room. |

Live location travels **only** over the P2P DataChannel, never the WebSocket:
`{ "t": "pos", "lat", "lng", "acc", "spd", "hdg", "ts" }`, broadcast every ~2s.

### Constraints

- **Room cap:** 8 peers (hard limit). **Per-IP:** 16 connections.
- **Max message size:** 4096 bytes (signaling only).
- **Heartbeat:** 60s pong timeout, 54s ping interval.
- **Rate limit:** 200 msgs/sec per connection (mesh setup is bursty).

## Running locally

The frontend is embedded in the binary (`//go:embed frontend`), so one process
serves both the app and `/ws` — zero setup:

```bash
go run .
# open http://localhost:8080 in two browser windows (or two devices on the LAN)
```

> Browser geolocation and the Wake Lock API require a **secure context**.
> `localhost` counts as secure, so local dev works. On a phone over the LAN you
> need HTTPS (e.g. a quick tunnel like `cloudflared`/`ngrok`) for GPS to work.

```bash
go test ./...     # hub routing, room isolation, opt-in isolation, 8-peer cap
docker build -t radar-server . && docker run -p 8080:8080 radar-server
```

## Deployment

Mirrors the sibling `share` project: **backend on Render, frontend on Cloudflare
Pages**, split across two hostnames.

### 1. Signaling server → Render

- Push this repo; in Render create a **Blueprint** (uses `render.yaml`) or a
  **Web Service** from the `Dockerfile`. Render injects `PORT`.
- Health check path: `/health`.
- Note the service URL, e.g. `radar-ws.zenlith.dev` (map a custom domain, or use
  the `*.onrender.com` host).

### 2. Frontend → Cloudflare Pages

- Deploy with **build output / publish directory = `frontend/`** and no build
  command (it's a static file). Point it at your domain, e.g. `radar.zenlith.dev`.
- Tell the client where the signaling server lives by editing the override map
  near the top of `frontend/index.html`:

  ```js
  const WS_HOST_OVERRIDES = {
      'radar.zenlith.dev': 'radar-ws.zenlith.dev'   // your Pages host → your Render host
  };
  ```

  Anywhere else (including `localhost`) the client just talks to its own origin,
  so local dev needs no override.

### External dependencies (client)

Loaded from CDN in `frontend/index.html`, pinned:
- **MapLibre GL JS 4.7.1** (jsDelivr) — map rendering.
- **CARTO dark basemap** raster tiles — the dark radar look. © OpenStreetMap
  contributors © CARTO. Swap the tile source in `initMap()` for a paid provider
  before any heavy production use.

## Security & privacy notes

- The relay sees only opaque WebRTC envelopes scoped to a room; cross-room
  delivery is dropped. Peer ids and room assignment are server-controlled and
  never trusted from the client.
- Anyone with the link can join (up to the cap). A "peer joined" toast fires on
  every arrival so an unexpected join is visible. Rotating the room is as simple
  as opening a fresh link.
- No cookies, no analytics, no storage — audit that this stays true when adding
  features.

## Roadmap / not-yet-built

- **QR code** on the invite screen for scan-to-join (currently: copy link +
  native share sheet).
- **PWA / installable** app to improve the background-GPS story on mobile.
- **Adaptive broadcast rate** (slower when stationary) to save battery.
- **Breadcrumb trails** (last N minutes, memory-only) for convoys.
- **Initiator approval** gate for joins on sensitive rooms.
