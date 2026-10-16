# Whiteboard

A real-time collaborative whiteboard. Many people draw on the same board at once;
every stroke, shape, text and cursor appears for everyone as it happens.

```
whiteboard-saas/
├── docker-compose.yml        2 API nodes + nginx, Postgres primary/replica, Redis, web
├── .env.example
├── deploy/
│   ├── nginx/nginx.conf      WebSocket-aware load balancer (least_conn)
│   └── postgres/             replication role + replica bootstrap scripts
├── backend/                  Go 1.24
│   ├── Dockerfile            multi-stage, distroless, non-root
│   ├── cmd/server/main.go    wiring, HTTP server, graceful shutdown
│   └── internal/
│       ├── config/           env configuration + validation
│       ├── logger/           Zap
│       ├── domain/           entities (User, Board, BoardSnapshot/JSONB) + repository ports
│       ├── repository/       GORM implementations (reads → replica, writes → primary)
│       ├── database/         postgres.go (master/replica via dbresolver), redis.go
│       ├── service/          use cases (guest sign-in, boards, snapshots)
│       ├── auth/             signed session tokens (HMAC-SHA256)
│       ├── canvas/           last-writer-wins board state (stdlib only, unit tested)
│       ├── pool/             bounded goroutine pool (stdlib only, unit tested)
│       ├── websocket/        hub.go, room.go, client.go, redis_pubsub.go, messages.go
│       └── transport/httpapi REST + WS upgrade, middleware, rate limits
└── frontend/                 Next.js 16 (App Router), TypeScript, Tailwind 4, react-konva
    ├── app/                  page.tsx (landing), board/[id]/page.tsx (room)
    ├── components/           WhiteboardCanvas.tsx, Tray.tsx, BoardChrome.tsx, …
    ├── hooks/                useWhiteboardSocket.ts, useBoard.ts, useSession.ts
    └── lib/                  boardState.ts (replication rules), geometry.ts, exportPng.ts, …
```

## Run it

```bash
cp .env.example .env          # then change the passwords and AUTH_SECRET
docker compose up --build
```

Open <http://localhost:3000>. Open the same board link in a second browser window to see
two people drawing together. The API is behind nginx on <http://localhost:8080>.

Local development without Docker for the web app: `cd frontend && npm install && npm run dev`
(expects the API on `localhost:8080`; set `NEXT_PUBLIC_API_URL` / `NEXT_PUBLIC_WS_URL` otherwise).

## How it works

**One room per board per node.** `Hub` holds a map of `Room`s guarded by a mutex. Each
`Room` is a single goroutine that owns its client set, so room logic needs no locks.
Each `Client` has exactly two goroutines (`readPump`, `writePump`); the room is the only
writer of a client's `send` channel. A client that cannot keep up is dropped instead of
stalling the room.

**Conflict-free sync.** Every shape has an id and a millisecond timestamp. The rule is
last-writer-wins per shape, deletions are tombstones, and "clear board" raises a
watermark. Because the rules do not depend on arrival order, messages can cross Redis,
worker goroutines and several nodes without coordination (`internal/canvas`,
mirrored in `frontend/lib/boardState.ts`). Clients also correct their clock from the
server's time and re-send their own latest writes after a reconnect, so a dropped
connection loses nothing.

**Many nodes.** A node publishes every room event on `wb:{boardId}:ch`; every node with
that room subscribed applies it and forwards it to its local clients (a node ignores its
own messages by node id). Redis also keeps a live copy of each active board
(`wb:{id}:els`, updated through a small Lua script so older writes never overwrite newer
ones) so a node that joins late starts from the current state, not the last snapshot.
Presence is a sorted set refreshed by every node, so people on a crashed node disappear
after 60 seconds.

**Persistence.** Edits mark the room dirty. A debounce timer (`SNAPSHOT_DEBOUNCE`, with a
`SNAPSHOT_MAX_WAIT` ceiling for constantly busy boards) submits one job to the shared
worker pool, which writes a JSONB snapshot to Postgres (last 10 are kept). Rooms flush a
final snapshot when they close or the server shuts down.

**Postgres primary / replica.** `database/postgres.go` registers GORM `dbresolver`:
reads go to the replica, writes to the primary. Paths that must see their own writes
(board lookup after create, snapshot load) are pinned to the primary. Compose starts a
real streaming replica with `pg_basebackup`.

**Graceful shutdown.** On SIGTERM the node reports not-ready, stops accepting HTTP,
tells every client to reconnect (close code 1012), flushes snapshots, drains the worker
pool and closes Redis and Postgres.

## Configuration (backend)

| Variable | Default | Meaning |
|---|---|---|
| `POSTGRES_MASTER_DSN` | required | primary connection string |
| `POSTGRES_REPLICA_DSN` | empty | replica; empty = reads use the primary |
| `REDIS_ADDRS` | `localhost:6379` | one address = standalone, several = Redis Cluster client |
| `AUTH_SECRET` | required, 32+ chars | signs session tokens |
| `ALLOWED_ORIGINS` | `http://localhost:3000` | CORS and WebSocket origin allow-list |
| `SNAPSHOT_DEBOUNCE` / `SNAPSHOT_MAX_WAIT` | `2s` / `15s` | snapshot timing |
| `WORKERS`, `POOL_QUEUE` | `8`, `1024` | worker pool size |
| `MAX_ROOM_CLIENTS` | `200` | people per board per node |
| `ROOM_IDLE_TIMEOUT` | `30s` | how long an empty room stays loaded |
| `TRUST_PROXY` | `false` | trust `X-Forwarded-For` for rate limiting (true behind nginx) |

## API

`POST /api/auth/guest {name}` → session · `GET /api/me` · `POST /api/boards {title}` ·
`GET /api/boards` · `GET /api/boards/{id}` · `GET /ws/boards/{id}?token=…` ·
`GET /healthz` · `GET /readyz`. The WebSocket protocol is documented at the top of
`backend/internal/websocket/messages.go`.

## Tests

```bash
cd backend && go test -race ./internal/canvas ./internal/pool ./internal/auth
cd frontend && npm test && npx tsc --noEmit
```

## Known limits

* Sign-in is by display name (guest accounts). Anyone with a board link can join; there is
  no per-board permission model yet.
* Compose runs a single Redis with AOF persistence. The code uses `redis.UniversalClient`
  and key hash-tags, so pointing `REDIS_ADDRS` at several cluster nodes works, but a
  cluster is not part of the compose file.
* Messages published while a node is briefly disconnected from Redis are not replayed;
  clients re-sync on their next reconnect.
