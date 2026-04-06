# zfs-nas-dashboard

[![CI](https://github.com/abaeyens/zfs-nas-dashboard/actions/workflows/ci.yml/badge.svg)](https://github.com/abaeyens/zfs-nas-dashboard/actions/workflows/ci.yml)

Read-only browser dashboard for a ZFS NAS.
Three panes — **Files**, **ZFS**, **Hardware** —
served from a single Docker container, no external dependencies.

<!-- screenshot -->

## Requirements

- Ubuntu/Debian host with ZFS
- Docker Engine + Compose v2
- Block device access for each disk in the pool (`/dev/sdX` or by-id)

## Setup

### 1. Configure pool and disks

Edit `docker-compose.yml` and set the four things specific to your system:

**Pool identity** — under `environment:`
```yaml
POOL_PATH: /vault   # absolute path where the pool is mounted on the host
POOL_NAME: vault    # name shown by `zpool list`
```

**Pool volume** — under `volumes:` (so the container can run `du`/`find`)
```yaml
- /vault:/vault:ro  # replace /vault with your POOL_PATH
```

**Disk devices** — under `devices:` (so `smartctl` can read SMART data):
```yaml
devices:
  - /dev/sda                              # block device for each disk in the pool
  - /dev/disk/by-id/ata-WDC_WD40EFAX-...  # corresponding stable by-id symlink
```
You need both the `/dev/sdX` entry (for kernel sysfs device-type detection) and the by-id symlink (used as the stable disk identifier in the UI). List available by-id names with:
```bash
ls -la /dev/disk/by-id/ | grep -v part
```

### 2. Build and bringup

```bash
docker compose up -d
```

and in your browser, open [http://localhost:8080](http://localhost:8080).

## Configuration

All settings are environment variables in `docker-compose.yml`:

| Variable | Default | Description |
|---|---|---|
| `POOL_PATH` | *(required)* | Absolute mount path of the pool (e.g. `/tank`) |
| `POOL_NAME` | *(required)* | ZFS pool name (e.g. `tank`) |
| `PORT` | `8080` | Port on which to serve the dashboard |
| `SCAN_DEPTH` | `5` | Directory scanning depth for the sunburst chart |
| `TEMP_HISTORY_HOURS` | `6` | Hours of disk temperature history to retain |
| `SMART_POLL_INTERVAL` | `60s` | How often to poll disk status |
| `FILES_REFRESH_INTERVAL` | `60s` | How often to update the sunburst files chart |
| `TEMP_WARN_C` | `45` | Disks temperature warning threshold (°C) |
| `TEMP_CRIT_C` | `55` | Disks temperature critical threshold (°C) |
| `REALLOC_WARN` / `REALLOC_CRIT` | `1` / `5` | Disks reallocated sectors thresholds |
| `PENDING_WARN` / `PENDING_CRIT` | `1` / `5` | Disks pending sectors thresholds |
| `UNCORR_WARN` / `UNCORR_CRIT` | `1` / `5` | Disks uncorrectable error thresholds |
| `DATA_DIR` | `/data` | Where to store database with temperature history |

---

## Development

The source tree is bind-mounted into the container at `/app`. The **running binary is baked into the image** at `/usr/local/bin/nas-dashboard` — after any source change you need to rebuild the image:

```bash
docker compose build && docker compose up -d
```

To ease development, the [Makefile](Makefile) provides the following shorthands:
| Command | Effect |
|---|---|
| `make up` | Start container |
| `make down` | Stop and remove container |
| `make shell` | Shell inside container |
| `make test` | Run all Go tests |
| `make build` | Recompile binary inside container (does not restart) |
| `make logs` | Follow container logs |


## API

| Endpoint | Description |
|---|---|
| `GET /api/hardware` | Latest SMART readings + temperature history |
| `GET /api/zfs` | Pool health, datasets, snapshots, ARC stats |
| `GET /api/files` | Directory tree + per-user usage (cached) |
| `GET /api/events` | SSE stream (`hardware`, `files`, `zfs` event types) |
| `GET /` | Embedded single-page frontend |


## Architecture

See [architecture.md](architecture.md) for the component design and data-flow diagram.

| Package | Role |
|---|---|
| `internal/config` | Parse env vars → typed `Config` struct |
| `internal/collector` | Pure functions: run system commands, return typed structs |
| `internal/store` | SQLite temperature history |
| `internal/broker` | SSE fan-out |
| `internal/poller` | Background goroutines, in-memory caches |
| `internal/handler` | HTTP router; reads caches, never calls collectors directly |
| `web/` | Embedded HTML/CSS/JS (ECharts), built into the binary |


## Issues and feature requests
Something not working as expected? Missing feature?
Please open a GitHub issue, or send me an email.
