# NAS dashboard — design considerations

Technical decisions and implementation details. For what the system must do, see
[requirements.md](requirements.md). For the project layout and component design, see
[architecture.md](architecture.md).

---

## Tech stack

| Layer | Choice | Notes |
|-------|--------|-------|
| Backend | **Go** (minimal deps: `zerolog`, `modernc.org/sqlite`) | Compiles to a single static binary; goroutines for background polling |
| Frontend | **Vanilla JS + HTML/CSS** | No build step; served as static files embedded in the Go binary |
| Charts | **Apache ECharts** (CDN) | Supports sunburst, pie, line charts; ships a Grafana-like dark theme |
| Real-time | **Server-Sent Events (SSE)** | Used for live temperature streaming; simple, no WebSocket overhead |
| Temp history | **SQLite** (`modernc.org/sqlite`) | Pure-Go, no CGo; DB file on a host-mounted volume; survives restarts |
| Deployment | **Docker** (host networking) | Isolates dependencies; avoids conflicts with other services on the host |

### Styling
- Dark colour scheme, inspired by Grafana (dark grey backgrounds, coloured metric values, subtle grid lines).
- ECharts built-in `dark` theme as the baseline; small amount of custom CSS for layout and cards.

---

## Data sources (shell commands)

| Data | Command / API |
|------|---------------|
| Directory tree sizes | `du -x --block-size=1 --max-depth N <path>` (bytes allocated on disk, reflects ZFS compression; omitting `-b`/`--apparent-size` ensures compressed size is reported) |
| File ownership / sizes | `find <path> -maxdepth N -printf '%u %b\n'` (owner + 512-byte blocks allocated on disk, reflects ZFS compression) |
| ZFS pool status & errors | `zpool status -p` |
| ZFS dataset list | `zfs list -o name,used,avail,refer,compressratio,compression` |
| ZFS snapshots | `zfs list -t snapshot -o name,used,creation -S creation` |
| Disk → vdev mapping | `zpool status -v` (parse vdev tree to get `/dev/disk/by-id/` names) |
| SMART data | `smartctl -A -j /dev/sdX` (JSON output; requires `smartmontools`) |
| SMART device info | `smartctl -i -j /dev/sdX` |
| ZFS ARC stats | `/proc/spl/kstat/zfs/arcstats` (Linux kernel ARC counters) |

---

## Deployment

- Packaged as a **Docker image** using a two-stage `Dockerfile`:
  1. **Build stage** (`golang:1.24-alpine`) — compiles the Go binary (`CGO_ENABLED=0`) with static files embedded via `embed.FS`. Produces a fully static binary.
  2. **Runtime stage** (`debian:bookworm-slim`) — installs `smartmontools`, `zfsutils-linux`, and `sudo` via `apt-get`; copies in the static binary. No Go toolchain in the final image.
- Container is run with **`--network host`** so it can bind to the desired port and reach host interfaces without NAT.
- Individual disk device files are bind-mounted into the container read-only using stable **`/dev/disk/by-id/`** paths (e.g. `ata-WDC_WD80EMAZ_XXXXXXXX`) rather than `/dev/sdX`, which can change after a drive replacement. The `docker-compose.yml` must be updated when drives are replaced.
- The host's `/proc/spl/kstat/zfs` is bind-mounted read-only for ARC stats.
- A host directory (e.g. `/opt/zfs-nas-dashboard/data`) is bind-mounted read-write for the SQLite database (`temps.db`).
- ZFS/SMART commands that require elevated privileges are handled via a narrow `sudoers` rule on the host (see Security section in requirements.md).
- Restart policy: **`restart: unless-stopped`** in `docker-compose.yml`. No separate systemd unit needed.
- A `tmpfs` is mounted at `/tmp` inside the container so the Go runtime and any tools requiring a writable temp directory work correctly with the read-only root filesystem.
- All required mounts, env vars, and restart policy are documented in `docker-compose.yml`.

---

## HTTP API

All endpoints are `GET`-only; any other method returns `405 Method Not Allowed`. Responses use `Content-Type: application/json` unless noted.

### `GET /`
Serves the single-page HTML application (embedded static file).

### `GET /api/files`
Returns directory tree and per-user disk usage. Always responds immediately with cached data.

```json
{
  "scanning": false,
  "cached_at": "2026-04-05T12:00:00Z",
  "tree": {
    "name": "tank", "path": "/tank", "size_bytes": 10995116277760,
    "children": [
      { "name": "media", "path": "/tank/media", "size_bytes": 5497558138880, "children": ["..."] }
    ]
  },
  "users": [
    { "user": "alice",  "size_bytes": 3298534883328, "status": "green" },
    { "user": "system", "size_bytes":    1073741824,  "status": "green" }
  ]
}
```

- `scanning: true` while a background scan is running; previously cached data is still served.
- `tree` and `users` are `null` only before the very first scan completes after startup.

### `GET /api/zfs`
Returns pool health, dataset list, snapshot list, and ARC stats.

```json
{
  "pool": {
    "name": "tank", "state": "ONLINE",
    "scan": { "type": "scrub", "state": "finished", "end_time": "2026-04-04T03:00:00Z", "errors": 0 },
    "vdevs": [
      { "name": "raidz2-0", "state": "ONLINE", "read_errors": 0, "write_errors": 0, "cksum_errors": 0 }
    ]
  },
  "datasets": [
    { "name": "tank/media", "used_bytes": 5497558138880, "avail_bytes": 4398046511104,
      "refer_bytes": 5368709120, "compress_ratio": 1.42, "compression": "lz4" }
  ],
  "snapshots": [
    { "name": "tank/media@2026-04-04", "dataset": "tank/media",
      "creation": "2026-04-04T03:00:00Z", "used_bytes": 1073741824 }
  ],
  "snapshot_count": 42,
  "snapshot_total_bytes": 12345678901,
  "arc": {
    "hit_rate": 0.94, "size_bytes": 12031262720,
    "total_ram_bytes": 17179869184, "status": "green"
  }
}
```

### `GET /api/hardware`
Returns current SMART readings for all disks in the pool.

```json
{
  "disks": [
    {
      "dev": "/dev/sda",
      "by_id": "/dev/disk/by-id/ata-WDC_WD80EMAZ_XXXXXXXX",
      "model": "WDC WD80EMAZ", "serial": "XXXXXXXX",
      "health": "PASSED",
      "celsius": 36,            "celsius_status": "green",
      "power_on_hours": 17520,
      "reallocated_sectors": 0, "reallocated_status": "green",
      "pending_sectors": 0,     "pending_status": "green",
      "uncorrectable_errors": 0,"uncorrectable_status": "green"
    }
  ]
}
```

### `GET /api/events` (SSE)
Server-Sent Events stream (`Content-Type: text/event-stream`). Kept open indefinitely; the backend pushes events every `SMART_POLL_INTERVAL` seconds.

**`init` event** — sent once immediately on (re)connect. Carries the full temperature history from SQLite to seed the chart, plus current SMART readings:

    event: init
    data: {"disks":["sda","sdb"],"history":{"sda":[{"ts":1743850800,"celsius":36},...],
           "sdb":[...]},"smart":<same shape as /api/hardware response>}

**`update` event** — sent on every subsequent SMART poll:

    event: update
    data: {"ts":1743854400,"temps":{"sda":36,"sdb":37},"smart":<same shape as /api/hardware response>}
