# zfs-nas-dashboard

[![CI](https://github.com/abaeyens/zfs-nas-dashboard/actions/workflows/ci.yml/badge.svg)](https://github.com/abaeyens/zfs-nas-dashboard/actions/workflows/ci.yml)

A read-only browser dashboard for a NAS running **Ubuntu/Debian** with a **ZFS RAIDZ2** pool. Three sections: **Files** (directory sunburst + per-user space), **ZFS** (pool health, datasets, snapshots, ARC), and **Hardware** (SMART cards + live temperature graph).

---

## Requirements

| | |
|---|---|
| Host OS | Ubuntu/Debian with ZFS |
| Docker | Docker Engine + Docker Compose v2 |
| Browser | Any modern browser on the local network |

---

## Quick start

### 1 — Clone and configure

```bash
git clone https://github.com/abaeyens/zfs-nas-dashboard.git
cd zfs-nas-dashboard
cp docker-compose.yml docker-compose.override.yml   # optional: local overrides
```

Edit `docker-compose.yml` (or your override file) and set at minimum:

```yaml
environment:
  POOL_PATH: /vault          # absolute path to your ZFS pool root
  POOL_NAME: vault           # zpool name
```

### 2 — Add your disk devices

The compose file needs `--device` entries for each disk in the pool so
`smartctl` can read SMART data.  Edit the `devices:` block:

```yaml
devices:
  - /dev/disk/by-id/ata-WDC_WD40EFAX-68JH4N0_WD-WX32D7088AF1
  - /dev/disk/by-id/ata-WDC_WD40EFRX-68N32N0_WD-WCC7K2RSYV6X
  # … one line per disk
```

Get the by-id names with:

```bash
ls -la /dev/disk/by-id/ | grep -v part
```

### 3 — Create the data directory

```bash
mkdir -p data
```

SQLite temperature history is stored in `./data/temps.db` (host-mounted).

### 4 — Build and run

```bash
docker compose up -d
```

Open [http://localhost:8080](http://localhost:8080) in your browser.

---

## Configuration

All settings are environment variables in `docker-compose.yml`.

| Variable | Default | Description |
|---|---|---|
| `POOL_PATH` | *(required)* | Absolute path to pool root (e.g. `/tank`) |
| `POOL_NAME` | *(required)* | ZFS pool name (e.g. `tank`) |
| `PORT` | `8080` | HTTP port |
| `SCAN_DEPTH` | `5` | Directory depth for the sunburst chart |
| `TEMP_HISTORY_HOURS` | `6` | Hours of temperature history to keep |
| `SMART_POLL_INTERVAL` | `60s` | SMART poll interval |
| `FILES_REFRESH_INTERVAL` | `60s` | Files background scan interval |
| `TEMP_WARN_C` | `45` | Temperature amber threshold (°C) |
| `TEMP_CRIT_C` | `55` | Temperature red threshold (°C) |
| `REALLOC_WARN` | `1` | Reallocated sectors amber threshold |
| `REALLOC_CRIT` | `5` | Reallocated sectors red threshold |
| `PENDING_WARN` | `1` | Pending sectors amber threshold |
| `PENDING_CRIT` | `5` | Pending sectors red threshold |
| `UNCORR_WARN` | `1` | Uncorrectable errors amber threshold |
| `UNCORR_CRIT` | `5` | Uncorrectable errors red threshold |
| `DATA_DIR` | `/data` | Path inside container for SQLite DB |

---

## Development

Work inside the container — the source tree is bind-mounted.

```bash
make up      # start container (stays alive; source is live-mounted)
make shell   # open a shell inside the container
make test    # run all Go tests
make build   # compile the binary
make logs    # follow container logs
make down    # stop and remove the container
```

---

## API

| Endpoint | Description |
|---|---|
| `GET /api/hardware` | Latest SMART readings + temperature history |
| `GET /api/zfs` | Pool health, datasets, snapshots, ARC stats |
| `GET /api/files` | Directory tree + per-user usage (cached) |
| `GET /api/events` | SSE stream: `init` on connect, `smart` on each poll |
| `GET /` | Embedded single-page frontend |

---

## Architecture

See [architecture.md](architecture.md) for the full component design and data flow diagram.

| Component | Role |
|---|---|
| `internal/config` | Parse env vars → typed `Config` struct |
| `internal/collector` | Pure functions: run system commands, return typed structs |
| `internal/store` | SQLite temperature history (thin wrapper) |
| `internal/broker` | SSE fan-out: `Register` / `Unregister` / `Broadcast` |
| `internal/poller` | Background goroutines; own the in-memory caches |
| `internal/handler` | HTTP router; reads caches, never calls collectors directly |
| `web/` | Embedded HTML/CSS/JS frontend (ECharts for charts) |

