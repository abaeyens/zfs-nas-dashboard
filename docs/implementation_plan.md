# NAS Dashboard — Implementation Plan

Build a containerised Go + Vanilla JS NAS dashboard from scratch, following
[requirements.md](requirements.md), [design_considerations.md](design_considerations.md),
and [architecture.md](architecture.md). Implement backend packages bottom-up, then the HTTP
layer, then the frontend. Each phase ends with a concrete verification step.

**Development model**: one `Dockerfile` and one `docker-compose.yml`, used for both
development and production. Build the image once; start the container; `docker exec` in and
work as if Docker isn't there. The source tree is bind-mounted so edits on the host are
immediately visible inside the container. The Go module cache lives in the container's
writable layer and survives restarts (avoid `docker compose down` to keep it).

---

## Decisions locked in

- `PENDING_CRIT=5` and `UNCORR_CRIT=5` added alongside existing `WARN` vars
- User disk `status` field: always `"green"` (reserved for future quota thresholds)
- Error state UX: red inline error banner with the error message inside the affected section

---

## Phase 1 — Scaffolding

*No logic — just project skeleton and container setup.*

1. `Dockerfile` — single stage, `golang:1.24-bookworm`; installs `smartmontools`, `zfsutils-linux`; `WORKDIR /app`; default `CMD ["go", "run", "./cmd/zfs-nas-dashboard"]`
   - Container runs as **root** for now — `smartctl` can access `/dev/disk/by-id/` directly, no sudoers rule needed
   - **TODO**: add `useradd -u 1000 app` + `USER app` + narrow sudoers rule on host (see Security section in requirements.md)
2. `docker-compose.yml` — one service; `network_mode: host`; bind-mounts source dir to `/app`, `/proc/spl/kstat/zfs` read-only, `/opt/zfs-nas-dashboard/data` to `/data`; per-disk device entries; all env vars with defaults; `restart: unless-stopped`
3. `docker compose build` — builds the image (downloads Go + apt deps once; cached thereafter)
4. `go mod init github.com/abaeyens/zfs-nas-dashboard` + `go.sum` — run inside container via `docker compose run --rm zfs-nas-dashboard go mod init ...`
5. Add deps: `go get github.com/rs/zerolog@latest modernc.org/sqlite@latest` — inside container
6. Create empty package stubs: `cmd/zfs-nas-dashboard/main.go`, `internal/config/`, `internal/collector/`, `internal/store/`, `internal/broker/`, `internal/poller/`, `internal/handler/`, `web/`
7. `web/embed.go` with `//go:embed` directive (package `web`)
8. `Makefile` — convenience targets: `shell` (`docker exec -it zfs-nas-dashboard bash`), `test` (`docker exec zfs-nas-dashboard go test ./...`), `logs` (`docker compose logs -f`), `up` / `down`

**Verify**: `docker compose up -d` starts the container; `docker exec -it zfs-nas-dashboard go build ./...` succeeds with empty stubs.

---

## Phase 2 — Config package

7. `internal/config/config.go`: parse all env vars into a typed `Config` struct; fail-fast on missing `POOL_PATH` / `POOL_NAME`.
   - Fields: `Port`, `PoolPath`, `PoolName`, `ScanDepth`, `TempHistoryHours`, `SmartPollInterval`, `FilesRefreshInterval`, `TempWarnC`, `TempCritC`, `ReallocWarn`, `ReallocCrit`, `PendingWarn`, `PendingCrit`, `UncorrWarn`, `UncorrCrit`, `DataDir`
   - All numeric vars parsed with `strconv`; defaults applied when env var is absent.

**Verify**: `docker exec zfs-nas-dashboard go test ./internal/config/...` — missing required var returns error; all defaults applied correctly.

---

## Phase 3 — Store package

8. `internal/store/store.go`:
   - `Open(dataDir string) (*Store, error)`: opens/creates the SQLite DB, runs `CREATE TABLE IF NOT EXISTS temps (ts INTEGER, disk TEXT, celsius REAL)`, prunes old rows immediately, schedules a daily prune goroutine.
   - `Insert(disk string, celsius float64)`: inserts a row with `time.Now().Unix()` as `ts`.
   - `GetSince(d time.Duration) ([]TempRow, error)`: queries rows where `ts > now − d`.
   - `Prune(cutoff time.Time) error`: deletes rows older than `cutoff`.
   - Corruption recovery: if `Open` fails due to SQLite errors, log a warning, delete the file, and retry once.

**Verify**: `docker exec zfs-nas-dashboard go test ./internal/store/...` — insert → `GetSince` returns the row; `Prune` removes old rows; corrupt DB is recreated automatically.

---

## Phase 4 — Collector package

*All three files are independent and can be implemented in parallel.*

9. `internal/collector/smart.go` — `Smart(cfg *config.Config) ([]DiskInfo, error)`:
   - Discovers disks from `zpool status` output (parse vdev tree for `/dev/disk/by-id/` paths).
   - Validates each device path against `^/dev/disk/by-id/[a-zA-Z0-9_.-]+$`.
   - Calls `smartctl -A -j`, `smartctl -i -j`, `smartctl -H -j` via `exec.Command` argument lists (never `sh -c`, never string interpolation).
   - Extracts SMART attrs 5, 9, 190, 194, 197, 198; falls back attr 190→194 for temperature if 194 is absent or zero.
   - Pre-computes `status` strings (`"green"` / `"amber"` / `"red"`) from config thresholds; the HTTP layer and frontend never apply threshold logic.

10. `internal/collector/zfs.go` — `ZFS(cfg *config.Config) (*ZFSResult, error)`:
    - Runs `zpool status -p`, `zfs list -o name,used,avail,refer,compressratio,compression`, `zfs list -t snapshot -o name,used,creation -S creation`.
    - Reads `/proc/spl/kstat/zfs/arcstats`; computes ARC hit rate as `hits / (hits + misses)` and ARC size vs total RAM.
    - Returns `ZFSResult{Pool, []Dataset, []Snapshot, ARC}`.

11. `internal/collector/files.go` — `Files(cfg *config.Config) (*FilesResult, error)`:
    - Runs `du -x --block-size=1 --max-depth N <POOL_PATH>` and `find <POOL_PATH> -maxdepth N -printf '%u %b\n'`.
    - Builds a `DirTree` struct (nested children) from `du` output.
    - Builds `[]UserUsage` from `find` output; groups owners with uid < 1000 into a single `"system"` entry.
    - `UserUsage.Status` is always `"green"`.

**Verify**: `docker exec zfs-nas-dashboard go test ./internal/collector/...` — tests use fixture command output injected via a `CommandRunner` func type; status-string logic validated with known threshold inputs. The container already has `zfs`, `zpool`, and `smartctl` available for any live integration tests.

---

## Phase 5 — Broker package

12. `internal/broker/broker.go`:
    - `New() *Broker`
    - `Register() <-chan []byte`: creates a buffered channel, registers it, returns it.
    - `Unregister(ch <-chan []byte)`: removes channel and closes it.
    - `Broadcast([]byte)`: non-blocking send to all registered channels; slow clients are dropped (not stalled).

**Verify**: `docker exec zfs-nas-dashboard go test ./internal/broker/...` — broadcast reaches N clients; slow/full-buffer client is dropped without blocking; `Unregister` cleans up correctly.

---

## Phase 6 — Poller package

13. `internal/poller/poller.go` — `Start(cfg, store, broker) *Poller`:
    - **SMART goroutine**: fires immediately on startup, then ticks every `SmartPollInterval`; calls `collector.Smart`; updates the SMART cache (guarded by `sync.RWMutex`); calls `store.Insert` for each disk temp; marshals an `update` SSE event and calls `broker.Broadcast`.
    - **Files goroutine**: ticks every `FilesRefreshInterval`; spawns an inner goroutine to call `collector.Files`; updates the files cache on completion; holds a `scanning` boolean flag (true while scan is running).
    - Read accessors: `LatestSMART() []DiskInfo`, `LatestFiles() FilesResult`, `IsScanning() bool`.

**Verify**: `docker compose up` (or `make up`), then `make logs` — logs confirm polls occur at correct intervals; cache accessors return fresh data.

---

## Phase 7 — HTTP handler

14. `internal/handler/handler.go` — `NewRouter(cfg, poller, broker) http.Handler`:
    - Method-guard middleware: any non-`GET` request returns `405 Method Not Allowed`.
    - `GET /` — serves `web.FS` `index.html` (embedded static file).
    - `GET /api/files` — returns `poller.LatestFiles()` as JSON, including `scanning` flag; always responds immediately.
    - `GET /api/zfs` — calls `collector.ZFS(cfg)` on every request (fast, no cache needed); on error returns `{"error":"<message>"}` with HTTP 500.
    - `GET /api/hardware` — returns `poller.LatestSMART()` as JSON.
    - `GET /api/events` — SSE (`Content-Type: text/event-stream`): sets required headers, calls `broker.Register`, immediately sends an `init` event (full temp history from `store.GetSince` + latest SMART), then loops on the channel until the client disconnects, then calls `broker.Unregister`.

15. `cmd/zfs-nas-dashboard/main.go`:
    - Wiring: `config.MustLoad()` → `store.Open()` → `broker.New()` → `poller.Start()` → `handler.NewRouter()` → `http.ListenAndServe()`.
    - Graceful shutdown on `SIGINT` / `SIGTERM`: stop poller goroutines, close store, let broker drain.

**Verify**: `docker exec zfs-nas-dashboard go build ./cmd/zfs-nas-dashboard` succeeds; `curl localhost:8080/api/zfs` from the host returns valid JSON (container uses `network_mode: host`).

---

## Phase 8 — Frontend

16. `web/index.html`:
    - Three column `<div>` elements (`#col-files`, `#col-zfs`, `#col-hardware`).
    - Bottom navigation bar (`<nav id="mobile-nav">`) with three icon+label buttons.
    - ECharts loaded via CDN `<script>` tag.
    - Placeholder `<div>` containers for each chart.

17. `web/style.css`:
    - CSS custom properties for the dark colour scheme (Grafana-inspired: dark grey backgrounds, coloured metric values, subtle grid lines).
    - CSS Grid layout: `grid-template-columns: repeat(3, 1fr)` at ≥ 768 px; single column + visible bottom nav at < 768 px.
    - Columns: `overflow-y: auto; max-height: 100vh`; the page itself does not scroll on desktop.
    - SMART card grid: `repeat(auto-fill, minmax(220px, 1fr))`.
    - Status utility classes: `.status-green`, `.status-amber`, `.status-red`.
    - Error banner: `.error-banner` — red background, full column width.
    - Mobile nav: fixed bottom bar, active tab highlighted.

18. `web/app.js`:
    - **Page load**: fetch `/api/files`, `/api/zfs`, `/api/hardware` concurrently; render all sections.
    - **Files section**: render ECharts sunburst from `tree` + pie chart from `users`; Refresh button triggers re-fetch; `setInterval` for background auto-refresh; show "Scanning…" indicator when `scanning === true`; show "last updated" timestamp.
    - **ZFS section**: render pool health + vdev table (amber/red rows for non-zero errors); dataset table with sortable columns; snapshots table (sorted newest-first, total count + size above); ARC stat cards with hit-rate colour coding.
    - **Hardware section**: render SMART cards; render real-time temp line chart. SSE loop via `new EventSource('/api/events')`:
      - `init` event: seeds line chart with full history + renders SMART cards.
      - `update` event: appends temp data point to chart + updates card field values in-place (no full re-render).
      - Auto-reconnect is native to `EventSource`; on reconnect the server sends `init` again to re-seed the chart.
    - **ECharts resize**: attach a `ResizeObserver` to each chart container `<div>`; call `chart.resize()` in the callback.
    - **Mobile nav**: click handlers show/hide columns via CSS class toggle; active button is highlighted.
    - **Error state**: any failed fetch replaces the section's content with a `.error-banner` `<div>` containing the error message.
    - Threshold colouring: map `status` strings from API responses to CSS classes. No threshold logic in JavaScript.

**Verify**: open browser; all three sections render with real data; temp chart updates every poll interval; mobile nav switches sections; chart resizes correctly on window resize.

---

## Phase 9 — Documentation & host setup

*Docker is already set up from Phase 1. This phase just documents the host prerequisites.*

19. `README.md`: host setup steps — `zfs allow` delegation command, sudoers snippet for `smartctl`, data directory creation (`/opt/zfs-nas-dashboard/data`), disk device paths to add to `docker-compose.yml`.

**Verify**: a fresh setup following only `README.md` works end-to-end.

---

## Files to create (all new)

| File | Phase |
|------|-------|
| `Dockerfile` | 1 |
| `docker-compose.yml` | 1 |
| `Makefile` | 1 |
| `go.mod`, `go.sum` | 1 |
| `web/embed.go` | 1 |
| `internal/config/config.go` | 2 |
| `internal/store/store.go` | 3 |
| `internal/collector/smart.go` | 4 |
| `internal/collector/zfs.go` | 4 |
| `internal/collector/files.go` | 4 |
| `internal/broker/broker.go` | 5 |
| `internal/poller/poller.go` | 6 |
| `internal/handler/handler.go` | 7 |
| `cmd/zfs-nas-dashboard/main.go` | 7 |
| `web/index.html` | 8 |
| `web/style.css` | 8 |
| `web/app.js` | 8 |
| `README.md` | 9 |

---

## End-to-end verification checklist

1. `docker compose build` → image built, no errors
2. `docker compose up -d` → container starts; `make logs` shows SMART + Files polls firing
4. `curl localhost:8080/api/files` → JSON with `tree` + `users`
5. `curl localhost:8080/api/zfs` → JSON with `pool`, `datasets`, `snapshots`, `arc`
6. `curl localhost:8080/api/hardware` → JSON with `disks` including `status` strings
7. Browser (desktop): three columns visible simultaneously; ECharts sunburst, pie, and line charts render; temp line chart updates on each poll
8. Browser (narrow viewport): bottom nav bar appears; tapping each button shows the correct section only
9. Corruption test: replace `data/temps.db` with a garbage file → server logs a warning, recreates the DB, continues
10. Command failure test: make `zfs` unavailable → ZFS section shows red error banner; other sections unaffected

---

## Scope exclusions

- No authentication or TLS (local network only)
- No click-to-drill-down on the sunburst chart
- No write or destructive ZFS operations of any kind
- No file manager or file download
- No alerting or notification system
