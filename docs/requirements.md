# NAS dashboard requirements

For technical decisions and implementation details see [design_considerations.md](design_considerations.md).
For project layout and component design see [architecture.md](architecture.md).

## Overview

A single-page, browser-based dashboard for a NAS running **Ubuntu/Debian** with a **ZFS RAIDZ2** pool.
No authentication required (local network only).

---

## Layout

The dashboard is a single HTML page with a **responsive two-tier layout**. There are no separate page loads — navigation only shows/hides sections.

### Desktop (viewport width ≥ 768 px)
- All three sections — **Files**, **ZFS**, and **Hardware** — are visible simultaneously as **equal-width columns** in a CSS Grid layout.
- No navigation bar needed; all content is on screen at once.
- Each column scrolls independently if its content exceeds the viewport height.

```
┌─────────────┬─────────────┬─────────────┐
│    Files    │     ZFS     │  Hardware   │
│             │             │             │
│  (sunburst) │ (pool/err.) │ (SMART cds) │
│  (user pie) │ (datasets)  │ (temp graph)│
│             │ (snapshots) │             │
│             │ (ARC stats) │             │
└─────────────┴─────────────┴─────────────┘
```

### Mobile (viewport width < 768 px, portrait phone)
- Only **one section is visible at a time**, occupying the full screen width.
- A **bottom navigation bar** with three icon+label buttons switches between sections:
  ```
  [ 📁 Files ]  [ 🗄 ZFS ]  [ 🖥 Hardware ]
  ```
- The active section is highlighted in the nav bar.
- Switching sections does not reload the page; it shows/hides the relevant column via CSS.
- Charts resize to fill the available width at the narrower viewport.

---

## Section: Files

### 1 — Sunburst (baobab-ring) chart
- Visualises the directory tree of the NAS data volume.
- Uses **ECharts `sunburst`** chart type.
- Scans `POOL_PATH` up to `SCAN_DEPTH` levels (default: 5), fetched in full on page load.
- Size shown = bytes allocated on disk (reflecting ZFS compression), sourced from `du -x --block-size=1`.
- Hovering a segment shows path and size (human-readable).
- Chart is **read-only** — no click-to-drill-down.

### 2 — Pie chart: used space per user
- Shows proportion of disk space owned by each UNIX user.
- Sourced from `find` traversal collecting owner + block count.
- Non-human/system users (uid < 1000, e.g. root, nobody, www-data) are **grouped into a single "system" slice**.
- Hovering shows username and size.

### Data refresh
- Both charts refresh on demand via a **Refresh** button.
- Background refresh every **`FILES_REFRESH_INTERVAL`** seconds (default: 60).
- Because `du`/`find` traversals on large pools can take tens of seconds, the backend runs them in a **background goroutine** and caches the result. HTTP requests always return the latest cached data immediately; a **"Scanning\u2026"** indicator is shown in the UI while the first scan is still in progress.
- A small "last updated" timestamp is shown beneath each chart.

---

## Section: ZFS

### 1 — Pool health & errors
- Displays pool name, state (ONLINE / DEGRADED / FAULTED), scan status and date.
- Table of vdevs with columns: **Name, State, Read errors, Write errors, Checksum errors**.
- Rows with any non-zero error counts are highlighted in amber/red.

### 2 — Dataset overview
- Table with columns: **Dataset, Used, Available, Referenced, Compression ratio, Algorithm**.
- Sortable columns.

### 3 — Snapshots
- Table with columns: **Snapshot name, Dataset, Creation date, Size**.
- Sorted by creation date descending.
- Total snapshot count and combined size shown above the table.

### 4 — ARC cache stats
- Two metrics displayed as simple stat cards on the ZFS tab:
  - **ARC hit rate** — percentage of reads served from cache (e.g. "94 %"). Computed as `hits / (hits + misses)` from `/proc/spl/kstat/zfs/arcstats`.
  - **ARC size** — current ARC memory usage vs total RAM (e.g. "11.2 GB / 16 GB").
- Colour-code hit rate: ≥ 80 % green, 50–80 % amber, < 50 % red.

### Data refresh
- Full tab refresh on page load and via a **Refresh** button.

---

## Section: Hardware

### 1 — Disk SMART summary cards
One card per disk in the ZFS pool (disks discovered via `zpool status`).

Each card shows:
| Field | SMART attribute / source |
|-------|--------------------------|
| Device path | `/dev/sdX` |
| Model / serial | `smartctl -i` |
| Current temperature | SMART attr 194 (try first); fall back to attr 190 if 194 is absent or zero |
| Reallocated sectors | SMART attr 5 |
| Pending sectors | SMART attr 197 |
| Uncorrectable errors | SMART attr 198 |
| Power-on hours | SMART attr 9 |
| Overall health | `smartctl -H` pass/fail |

- Temperature and error counts are colour-coded: green → amber → red thresholds (configurable via env vars).
- The **backend pre-computes a `status` string** (`"green"` / `"amber"` / `"red"`) for each metric and includes it in every API response; the frontend maps status strings to CSS classes. Threshold logic lives exclusively in the backend.
- Cards are arranged in a responsive grid (2–4 columns depending on viewport width).

### 2 — Temperature graph (real-time)
- **ECharts `line`** chart showing temperature of each disk over the last N hours (default: 6 h).
- One line per disk, coloured distinctly.
- Backend polls SMART every **`SMART_POLL_INTERVAL`** seconds (default: 60) and pushes an SSE event.
- On connect (or reconnect), the backend immediately emits an **`init`** event containing the full temperature history from SQLite (to seed the chart) plus current SMART readings. Subsequent events are incremental **`update`** events.
- Each `update` event carries **both** a new temperature data point and updated SMART readings for all cards.
- Frontend appends the temperature point to the chart and refreshes the SMART card values — no full page redraw.
- The SSE connection auto-reconnects if dropped; on reconnect the `init` event re-seeds the chart with the latest history.
- X-axis: wall-clock time. Y-axis: °C.
- History is persisted to a **SQLite database** (`data/temps.db`) on a host-mounted volume.
- Schema: `CREATE TABLE temps (ts INTEGER, disk TEXT, celsius REAL);`
- Each 60-second poll inserts a row directly — no periodic flush needed.
- On startup, the backend queries rows where `ts > now - 6h`; older rows are pruned at startup.
- If the DB file is missing it is created automatically; if corrupt the backend recreates it and starts fresh (no crash).

---

## Configuration (environment variables)

All tunable values are set via environment variables in `docker-compose.yml`. Defaults are shown.

| Variable | Default | Description |
|----------|---------|-------------|
| `POOL_PATH` | *(required)* | Absolute path to the ZFS pool root (e.g. `/tank`) |
| `POOL_NAME` | *(required)* | ZFS pool name (e.g. `tank`) |
| `PORT` | `8080` | HTTP port the server listens on |
| `SCAN_DEPTH` | `5` | Directory levels to scan for the sunburst chart |
| `TEMP_HISTORY_HOURS` | `6` | Hours of temperature history to retain |
| `SMART_POLL_INTERVAL` | `60` | Seconds between SMART/temperature polls |
| `FILES_REFRESH_INTERVAL` | `60` | Seconds between background Files tab refreshes |
| `TEMP_WARN_C` | `45` | Temperature threshold: green → amber (°C) |
| `TEMP_CRIT_C` | `55` | Temperature threshold: amber → red (°C) |
| `REALLOC_WARN` | `1` | Reallocated sectors threshold: green → amber |
| `REALLOC_CRIT` | `5` | Reallocated sectors threshold: amber → red |
| `PENDING_WARN` | `1` | Pending sectors: green → amber |
| `UNCORR_WARN` | `1` | Uncorrectable errors: green → amber |
| `DATA_DIR` | `/data` | Path inside container for the SQLite DB (bind-mounted) |

---

## Security

### Principle: read-only, least privilege
The dashboard is purely a monitoring tool. No write operations are ever performed. The architecture is designed so that even a fully compromised server process cannot destroy ZFS data.

### Container process
- Runs as a **non-root user** (uid 1000) inside the container.
- No `--privileged` flag. No `--cap-add SYS_ADMIN` or other broad capabilities.
- The container's root filesystem is **read-only** (`read_only: true` in `docker-compose.yml`); only `/data` (the SQLite volume) and `/tmp` are writable.

### ZFS access (`zpool status`, `zfs list`)
- Use **ZFS delegation** on the host to grant the container user read-only permissions for `zfs` subcommands — no root required:
  ```
  zfs allow -u <uid> list,get,hold <poolname>
  ```
- `zpool status` (a separate binary from `zfs`) does **not** use ZFS delegation but is readable by any non-root user by default on Linux OpenZFS — no extra configuration needed.
- `zfs` destructive subcommands (`destroy`, `rollback`, `export`, etc.) are unreachable because the delegated permissions exclude them.

### SMART access (`smartctl`)
- `smartctl` requires raw disk access (root or `disk` group). This is the **only** command that needs privilege.
- A narrow `sudoers` rule on the host allows the container user to run only specific `smartctl` invocations:
  ```
  nas-dashboard ALL=(root) NOPASSWD: /usr/sbin/smartctl -A -j /dev/sd*, \
                                      /usr/sbin/smartctl -i -j /dev/sd*, \
                                      /usr/sbin/smartctl -H -j /dev/sd*
  ```
- The Go process calls `sudo smartctl ...` with **argument lists only** — never via `sh -c` or string interpolation — eliminating shell injection.

### Shell injection prevention
- Every `os/exec` call in the Go code uses **`exec.Command(binary, arg1, arg2, ...)`** with arguments as separate parameters.
- `POOL_NAME`, `POOL_PATH`, and device paths are **never interpolated into a shell string**.
- Device paths discovered from `zpool status` output are validated against the pattern `/dev/sd[a-z]+` before being used in any command.

### `/dev` mount
- Only individual disk device files are bind-mounted into the container read-only — **not all of `/dev`**.
- Stable **`/dev/disk/by-id/`** paths are used (consistent with the Deployment section), not `/dev/sdX` which is not persistent across reboots or drive replacements.

### Network exposure
- The dashboard listens on `PORT` (default 8080) on all interfaces due to `--network host`.
- It is expected to be firewalled to the local network at the host level. No TLS is provided (local network only, per requirements).
- The HTTP server only exposes `GET` endpoints; any non-GET request returns 405.

---

## Non-functional requirements

- Backend runs as a non-root user; privilege is limited to a narrow `sudoers` rule for `smartctl` only (see Security section). ZFS commands run unprivileged via ZFS delegation.
- All long-running shell commands have a **timeout** (default 30 s) to avoid hanging the server.
- The dashboard must remain usable if one data source fails: the affected section shows a generic **error icon and "Data unavailable" label** and recovers automatically on the next auto-refresh cycle. Other sections are unaffected.
- Target load time for each tab: < 3 s on a local gigabit network.

---

## Future / out of scope (for now)

- User authentication.
- Write operations (scrub triggers, snapshot deletion, etc.).
- Alerting / notifications.

