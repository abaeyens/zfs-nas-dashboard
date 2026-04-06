# NAS dashboard — architecture

## Project layout

```
nas-dashboard/
│
├── cmd/
│   └── nas-dashboard/
│       └── main.go            # entry point: wires config → store → broker → poller → handler
│
├── internal/
│   ├── config/
│   │   └── config.go          # parses and validates env vars into a Config struct; fails fast on missing required vars
│   │
│   ├── collector/
│   │   ├── files.go           # du + find → DirTree, []UserUsage
│   │   ├── zfs.go             # zpool status, zfs list, arcstats → Pool, []Dataset, []Snapshot, ARC
│   │   └── smart.go           # smartctl -A/-i/-H → []DiskInfo (with pre-computed status strings)
│   │
│   ├── store/
│   │   └── store.go           # Open/Close, Insert, GetSince, Prune (SQLite via modernc.org/sqlite)
│   │
│   ├── broker/
│   │   └── broker.go          # Register/Unregister SSE client channels, Broadcast([]byte)
│   │
│   ├── poller/
│   │   └── poller.go          # StartSMART, StartFiles goroutines; own the in-memory caches
│   │
│   └── handler/
│       └── handler.go         # NewRouter; GET /api/files, /api/zfs, /api/hardware, /api/events, GET /
│
├── web/
│   ├── embed.go               # package web; //go:embed *.html *.js *.css; var FS embed.FS
│   ├── index.html
│   ├── style.css
│   └── app.js
│
├── Dockerfile
├── docker-compose.yml
├── .dockerignore
├── Makefile                   # targets: build, image, run
├── go.mod
└── go.sum
```

---

## Component responsibilities

### `config`
- Reads all env vars at startup, converts them to typed fields (ints, strings, durations).
- Validates required vars (`POOL_PATH`, `POOL_NAME`); exits with a clear error message if absent.
- No logic beyond parsing and validation. Everything that needs configuration receives a `*config.Config`.

### `collector`
- Pure functions: accept a `*config.Config`, run one or more shell commands, return typed structs or an error.
- No goroutines, no state, no caches. Fully unit-testable by injecting a fake command runner.
- All `os/exec` calls use argument lists only — never `sh -c` or string interpolation.
- Device paths from `zpool` output are validated against `/dev/disk/by-id/[a-zA-Z0-9_-]+` before use.
- `smart.go` pre-computes `status` strings (`"green"` / `"amber"` / `"red"`) using threshold values from config; the HTTP layer and frontend never apply threshold logic themselves.

### `store`
- Thin wrapper around a single SQLite table: `CREATE TABLE temps (ts INTEGER, disk TEXT, celsius REAL)`.
- Exposes four methods: `Open`, `Insert`, `GetSince(duration)`, `Prune(cutoff)`.
- Called only by `poller`. Handlers never touch the store directly.

### `broker`
- Fan-out hub for SSE events.
- `Register()` returns a `<-chan []byte`; `Unregister()` removes the channel and closes it.
- `Broadcast([]byte)` sends to all registered channels in a non-blocking way (slow clients are dropped, not stalled).
- Knows nothing about SMART or ZFS — operates on raw serialised JSON bytes.

### `poller`
- Owns two long-running goroutines launched at startup:
  - **SMART poller**: ticks every `SMART_POLL_INTERVAL`; calls `collector.Smart`, updates the SMART cache, inserts into the store, calls `broker.Broadcast`.
  - **Files poller**: ticks every `FILES_REFRESH_INTERVAL`; calls `collector.Files` in the background; updates the files cache on completion.
- Caches are protected by `sync.RWMutex`; handlers acquire a read lock to copy the latest snapshot.
- Exposes read-only accessors: `LatestSMART() []DiskInfo`, `LatestFiles() FilesResult`, `TempsHistory(duration) []TempRow` etc.

### `handler`
- `NewRouter(cfg, poller, broker)` returns an `http.Handler`.
- Each API handler reads from `poller` accessors (never calls collectors directly) and writes JSON.
- SSE handler: registers with `broker`, sends `init` snapshot immediately, then loops on the channel until the client disconnects.
- All handlers check the request method and return `405` for anything other than `GET`.

### `web`
- Static files served from `web.FS` (embedded at build time via `//go:embed`).
- `embed.go` is a minimal file in `package web` — the directive must live in the same package as the files it embeds.
- A single `app.js` handles all three sections, the SSE connection, ECharts initialisation, and mobile nav. Natural split point later if it grows unwieldy: `files.js`, `zfs.js`, `hardware.js`, `sse.js`.

---

## Data flow

```
                  ┌──────────────────────────────────────────┐
  Shell commands  │              poller goroutines           │
  (zfs, du, find) │  ┌───────────────────────────────────┐   │
  ────────────────┼─▶│  collector (pure functions)       │   │
                  │  └──────────────┬────────────────────┘   │
                  │                 │ typed structs          │
                  │  ┌──────────────▼────────────────────┐   │
                  │  │  update cache (sync.RWMutex)      │   │
                  │  └──────────────┬────────────────────┘   │
                  │                 │ temp readings only     │
                  │  ┌──────────────▼────────────────────┐   │
                  │  │  store.Insert (SQLite)            │   │
                  │  └──────────────┬────────────────────┘   │
                  │                 │ serialised JSON        │
                  │  ┌──────────────▼────────────────────┐   │
                  │  │  broker.Broadcast                 │   │
                  │  └──────────────┬────────────────────┘   │
                  └─────────────────┼────────────────────────┘
                                    │ []byte per connected client
                  ┌─────────────────▼────────────────────────┐
                  │           handler (HTTP layer)           │
                  │  /api/files      reads files cache       │
                  │  /api/zfs        calls collector.ZFS     │
                  │  /api/hardware   reads SMART cache       │
                  │  /api/events     SSE: init + update loop │
                  └──────────────────────────────────────────┘
```

Note: `/api/zfs` calls the ZFS collector on every request (ZFS data is fast to retrieve and rarely changes), while `/api/files` and `/api/hardware` always return cached data.

---

## `main.go` wiring (pseudocode)

```go
cfg  := config.MustLoad()
st   := store.MustOpen(cfg.DataDir)
brk  := broker.New()
poll := poller.Start(cfg, st, brk)
rtr  := handler.NewRouter(cfg, poll, brk)
log.Info().Str("port", cfg.Port).Msg("listening")
http.ListenAndServe(":"+cfg.Port, rtr)
```

---

## Key design decisions

| Decision | Rationale |
|----------|-----------|
| Collectors are pure functions, no state | Testable without a real ZFS pool; poller owns all state |
| Backend pre-computes `status` strings | Threshold logic in one place (backend); frontend is dumb CSS mapper |
| Broker operates on `[]byte` | Decoupled from domain types; trivially testable |
| Caches in poller, not in a `cache/` package | The poller is the sole writer; no need to abstract a single-writer pattern |
| `embed.go` lives in `package web` | `//go:embed` directive must be co-located with the files it embeds |
| `/api/zfs` calls collector on each request | ZFS commands are fast (\<1 s); caching adds complexity for no real benefit |
| Single `app.js` for now | ~500–700 lines is manageable; split if it grows |
| No `pkg/` directory | All packages are `internal/`; nothing is intended for external import |
