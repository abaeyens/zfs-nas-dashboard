/* NAS Dashboard — app.js
 * Three sections: Files, ZFS, Hardware.
 * Data sources:
 *   GET /api/files    → FilesResult  { tree, users }
 *   GET /api/zfs      → ZFSResult    { pool, datasets, snapshots, arc, ... }
 *   SSE /api/events   → { type:"init"|"smart", disks, history? }
 */

'use strict';

// ── Utilities ──────────────────────────────────────────────────────────────

// esc sanitises a value for safe inclusion in HTML attribute values and
// element text content, preventing stored XSS from data returned by the API.
function esc(val) {
  return String(val ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function fmtBytes(n) {
  if (n == null) return '—';
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) {
    n /= 1024;
    i++;
  }
  return n.toFixed(i === 0 ? 0 : 1) + ' ' + u[i];
}

function fmtGB(n) {
  if (n == null) return '—';
  return Math.round(n / 1024 ** 3) + ' GB';
}

function fmtTime(isoOrUnix) {
  if (!isoOrUnix) return '—';
  const n = Number(isoOrUnix);
  const d = !isNaN(n) ? new Date(n * 1000) : new Date(isoOrUnix);
  if (isNaN(d)) return '—';
  const pad = (v) => String(v).padStart(2, '0');
  return (
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ` +
    `${pad(d.getHours())}:${pad(d.getMinutes())}`
  );
}

function pill(status) {
  status = (status || 'unknown').toLowerCase();
  const cls =
    status === 'green'
      ? 'pill-green'
      : status === 'amber'
        ? 'pill-amber'
        : status === 'red'
          ? 'pill-red'
          : status === 'online'
            ? 'pill-online'
            : status === 'degraded'
              ? 'pill-degraded'
              : status === 'faulted'
                ? 'pill-faulted'
                : 'pill-amber';
  return `<span class="pill ${cls}">${esc(status.toUpperCase())}</span>`;
}

function statusClass(s) {
  return (s || '').toLowerCase(); // "green" | "amber" | "red"
}

function setUpdated(id) {
  const el = document.getElementById(id);
  if (el) el.textContent = 'Updated ' + new Date().toLocaleTimeString();
}

// ── Mobile nav ─────────────────────────────────────────────────────────────

const MOBILE_TABS = ['files', 'zfs', 'hardware'];

function initMobileNav() {
  const buttons = document.querySelectorAll('.mobile-nav .nav-btn');

  function activateMobile(target) {
    buttons.forEach((b) => b.classList.toggle('active', b.dataset.target === target));
    document.querySelectorAll('.panel').forEach((p) => {
      p.classList.toggle('active', p.dataset.name === target);
    });
    sessionStorage.setItem('mobileTab', target);
  }

  buttons.forEach((btn) => {
    btn.addEventListener('click', () => {
      location.hash = btn.dataset.target;
    });
  });

  window.addEventListener('hashchange', () => {
    const hash = location.hash.slice(1);
    if (MOBILE_TABS.includes(hash)) activateMobile(hash);
  });

  const hash = location.hash.slice(1);
  const initial = MOBILE_TABS.includes(hash)
    ? hash
    : sessionStorage.getItem('mobileTab') || 'files';
  history.replaceState(null, '', '#' + initial);
  activateMobile(initial);
}

// ── Medium nav ─────────────────────────────────────────────────────────────

// Maps a canonical panel hash (files/zfs/hardware) to a medium-nav page target.
function hashToMediumTarget(hash) {
  if (hash === 'files') return 'files';
  if (hash === 'zfs' || hash === 'hardware') return 'system';
  return null;
}

function initMediumNav() {
  const layout = document.getElementById('layout');
  const buttons = document.querySelectorAll('.medium-nav .nav-btn');

  function activateMedium(target) {
    buttons.forEach((b) => b.classList.toggle('active', b.dataset.target === target));
    layout.classList.toggle('page-files', target === 'files');
    layout.classList.toggle('page-system', target === 'system');
    sessionStorage.setItem('mediumPage', target);
    // Resize charts after panels become visible
    setTimeout(() => {
      sunburstChart && sunburstChart.resize();
      userPieChart && userPieChart.resize();
      tempChart && tempChart.resize();
    }, 50);
  }

  buttons.forEach((btn) => {
    // System button writes #zfs as the canonical hash for the system page.
    const hash = btn.dataset.target === 'system' ? 'zfs' : btn.dataset.target;
    btn.addEventListener('click', () => {
      location.hash = hash;
    });
  });

  window.addEventListener('hashchange', () => {
    const target = hashToMediumTarget(location.hash.slice(1));
    if (target) activateMedium(target);
  });

  const hash = location.hash.slice(1);
  const fromHash = hashToMediumTarget(hash);
  const initial = fromHash || sessionStorage.getItem('mediumPage') || 'files';
  activateMedium(initial);
}

// ── ECharts helpers ────────────────────────────────────────────────────────

let sunburstChart = null;
let userPieChart = null;
let tempChart = null;
// Fixed palette for sunburst/directory colors (original ECharts hex palette)
const PALETTE = [
  '#5470c6',
  '#91cc75',
  '#fac858',
  '#ee6666',
  '#73c0de',
  '#3ba272',
  '#fc8452',
  '#9a60b4',
  '#ea7ccc',
];

// Generate n disk colors sweeping from red (29°) to reddish-blue (275°)
function diskColorsFor(n) {
  const count = n || 1;
  const startH = 29,
    endH = 255;
  return Array.from({ length: count }, (_, i) => {
    const h = count === 1 ? startH : Math.round(startH + (i * (endH - startH)) / (count - 1));
    return `oklch(78% 0.37 ${h}deg)`;
  });
}

// Convert an oklch(...) string to rgba(r,g,b,alpha) so table text matches
// the chart line color at the same opacity.
function oklchToRgba(oklchStr, alpha) {
  const m = oklchStr.match(/oklch\(([\d.]+)%\s+([\d.]+)\s+([\d.]+)deg\)/);
  if (!m) return oklchStr;
  const L = +m[1] / 100,
    C = +m[2],
    H = (+m[3] * Math.PI) / 180;
  // OKLCH → OKLAB
  const a = C * Math.cos(H),
    b = C * Math.sin(H);
  // OKLAB → LMS (cubed)
  const l_ = L + 0.3963377774 * a + 0.2158037573 * b;
  const m_ = L - 0.1055613458 * a - 0.0638541728 * b;
  const s_ = L - 0.0894841775 * a - 1.291485548 * b;
  const lv = l_ * l_ * l_,
    mv = m_ * m_ * m_,
    sv = s_ * s_ * s_;
  // LMS → linear sRGB
  const lr = 4.0767416621 * lv - 3.3077115913 * mv + 0.2309699292 * sv;
  const lg = -1.2684380046 * lv + 2.6097574011 * mv - 0.3413193965 * sv;
  const lb = -0.0041960863 * lv - 0.7034186147 * mv + 1.707614701 * sv;
  // Linear sRGB → gamma-corrected sRGB
  const toSRGB = (c) => {
    c = Math.max(0, Math.min(1, c));
    return c <= 0.0031308 ? 12.92 * c : 1.055 * Math.pow(c, 1 / 2.4) - 0.055;
  };
  const r = Math.round(toSRGB(lr) * 255);
  const g = Math.round(toSRGB(lg) * 255);
  const bv = Math.round(toSRGB(lb) * 255);
  return `rgba(${r},${g},${bv},${alpha})`;
}
let diskColorMap = {}; // by_id → oklch color string
let byIdToDevMap = {}; // by_id → dev short name (e.g. 'sda')
let smartPollIntervalS = 60; // updated from server on init/hardware load
let tempHistoryHours = 6; // updated from server on init/hardware load

// Sunburst drill state
let _sunburstRootStack = [];
let _sunburstCurrentRoot = null;

function initCharts() {
  sunburstChart = echarts.init(document.getElementById('chart-sunburst'), 'dark');
  userPieChart = echarts.init(document.getElementById('chart-userpie'), 'dark');
  tempChart = echarts.init(document.getElementById('chart-temps'), 'dark');

  const ro = new ResizeObserver(() => {
    sunburstChart && sunburstChart.resize();
    userPieChart && userPieChart.resize();
    tempChart && tempChart.resize();
  });
  document.querySelectorAll('.chart').forEach((el) => ro.observe(el));

  // Custom drill-down: click a segment with children to zoom in
  sunburstChart.on('click', (params) => {
    if (params.componentType !== 'series') return;
    const node = params.data;
    if (!node || !node.children || node.children.length === 0) return;
    _sunburstRootStack.push(_sunburstCurrentRoot);
    renderSunburstAt(node);
  });

  document.getElementById('sunburst-back').addEventListener('click', () => {
    if (_sunburstRootStack.length > 0) renderSunburstAt(_sunburstRootStack.pop());
  });
}

// ── Files section ──────────────────────────────────────────────────────────

function loadFiles() {
  document.getElementById('files-scanning').hidden = false;
  fetch('/api/files')
    .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
    .then((data) => {
      document.getElementById('files-scanning').hidden = true;
      renderSunburst(data);
      renderUserPie(data);
      setUpdated('files-updated');
    })
    .catch((err) => {
      document.getElementById('files-scanning').hidden = false;
      document.getElementById('files-scanning').textContent = 'Error loading files: ' + err;
    });
}

function treeToSunburst(node, colorIndex, depth) {
  if (!node) return null;
  if (depth !== undefined && depth <= 0) return null;
  const out = {
    name: node.name || node.path || '/',
    value: node.size_bytes,
    itemStyle: {},
  };
  if (colorIndex !== undefined) {
    out.itemStyle.color = PALETTE[colorIndex % PALETTE.length];
  }
  if (node.children && node.children.length && (depth === undefined || depth > 1)) {
    out.children = node.children
      .map((c, i) =>
        treeToSunburst(
          c,
          colorIndex !== undefined ? colorIndex : i,
          depth !== undefined ? depth - 1 : undefined
        )
      )
      .filter(Boolean);
  }
  return out;
}

function renderSunburst(filesData) {
  if (!filesData || !sunburstChart) return;
  _sunburstRootStack = [];

  const tree = filesData.tree;
  if (!tree) return;
  const root = treeToSunburst(tree, undefined, 4);
  if (!root) return;

  // Assign distinct colors to top-level directory children
  if (root.children) {
    root.children.forEach((c, i) => {
      c.itemStyle = { color: PALETTE[i % PALETTE.length] };
    });
  }

  // Record directory-only total for the 20% threshold (before adding Avail/Snapshots)
  root._dirTotal = root.value || 1;

  const snapshotBytes = filesData.snapshot_bytes || 0;
  const availBytes = filesData.avail_bytes || 0;
  if (snapshotBytes > 0) {
    if (!root.children) root.children = [];
    root.children.push({
      name: 'Snapshots & Trashed',
      value: snapshotBytes,
      itemStyle: { color: '#8c8c8c' },
      _special: true,
    });
    root.value = (root.value || 0) + snapshotBytes;
  }
  if (availBytes > 0) {
    if (!root.children) root.children = [];
    root.children.push({
      name: 'Available',
      value: availBytes,
      itemStyle: { color: '#2d4a2d' },
      _special: true,
    });
    root.value = (root.value || 0) + availBytes;
  }

  renderSunburstAt(root);
}

// Render the sunburst focused on rootNode (its children become the first ring).
function renderSunburstAt(rootNode) {
  if (!sunburstChart || !rootNode) return;
  _sunburstCurrentRoot = rootNode;

  const firstRing = rootNode.children || [];
  // Threshold denominator: use _dirTotal if present (top-level), else sum of siblings
  const threshold =
    rootNode._dirTotal ||
    firstRing.filter((c) => !c._special).reduce((s, c) => s + (c.value || 0), 0) ||
    1;

  // Apply per-node label config so the 20% rule is re-evaluated for this ring
  firstRing.forEach((c) => {
    if (c._special || c.value / threshold >= 0.125) {
      c.label = {
        show: true,
        fontSize: 14,
        position: 'outside',
        formatter: () => `${c.name}\n${fmtBytes(c.value)}`,
      };
    } else {
      c.label = { show: false };
    }
  });

  // Show back button only when drilled in
  const backBtn = document.getElementById('sunburst-back');
  if (backBtn) backBtn.style.display = _sunburstRootStack.length > 0 ? '' : 'none';

  // clear() fully resets emphasis/blur state before rendering new data
  sunburstChart.clear();
  sunburstChart.setOption({
    backgroundColor: 'transparent',
    tooltip: { trigger: 'item', formatter: (p) => `${p.name}<br/>${fmtBytes(p.value)}` },
    series: [
      {
        type: 'sunburst',
        data: firstRing,
        radius: ['10%', '85%'],
        nodeClick: false,
        label: { show: false },
        emphasis: { focus: 'ancestor' },
        levels: [{}, { r0: '10%', r: '40%' }, { r0: '40%', r: '65%' }, { r0: '65%', r: '85%' }],
      },
    ],
  });

  // Clear any lingering emphasis/blur state from the click that triggered the drill
  sunburstChart.dispatchAction({ type: 'downplay' });
}

function renderUserPie(filesData) {
  const users = filesData && filesData.users;
  if (!users || !userPieChart) return;
  const items = users.map((u) => ({ name: u.user, value: u.size_bytes }));
  userPieChart.setOption({
    backgroundColor: 'transparent',
    tooltip: {
      trigger: 'item',
      formatter: (p) => `${p.name}<br/>${fmtBytes(p.value)} (${p.percent.toFixed(1)}%)`,
    },
    legend: { show: false },
    series: [
      {
        type: 'pie',
        data: items,
        radius: ['35%', '70%'],
        label: { fontSize: 14, formatter: (p) => `${p.name}\n${fmtBytes(p.value)}` },
        emphasis: { itemStyle: { shadowBlur: 6 } },
      },
    ],
  });
}

// ── ZFS section ────────────────────────────────────────────────────────────

function loadZFS() {
  fetch('/api/zfs')
    .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
    .then((data) => {
      renderZFSPool(data.pool);
      renderZFSARC(data.arc);
      renderZFSDatasets(data.datasets);
      renderZFSSnapshots(data.snapshots, data.snapshot_count, data.snapshot_total_bytes);
      setUpdated('zfs-updated');
    })
    .catch((err) => console.error('ZFS load error:', err));
}

function renderZFSPool(pool) {
  if (!pool) return;
  const el = document.getElementById('zfs-pool-content');
  const stateCls = pool.state ? pool.state.toLowerCase() : 'unknown';
  // Populate scrub info in card-header
  const scrubEl = document.getElementById('pool-scrub-info');
  if (scrubEl) {
    let scrubText = '';
    if (pool.scan && pool.scan.type) {
      scrubText = pool.scan.type + ' ' + pool.scan.state;
      if (pool.scan.end_time) {
        const d = new Date(pool.scan.end_time);
        if (!isNaN(d)) {
          const pad = (v) => String(v).padStart(2, '0');
          scrubText += ' ' + d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate());
        }
      }
    }
    scrubEl.textContent = scrubText;
  }

  const clean = !pool.errors_msg || /no known data errors/i.test(pool.errors_msg);
  const errorsPill = `<span class="pill ${clean ? 'pill-green' : 'pill-red'}">${clean ? 'CLEAN' : 'DATA ERRORS'}</span>`;
  let html = `<div class="pool-meta">
    <span class="state">${esc(pool.name)}</span>
    ${pill(pool.state)}
    ${errorsPill}
  </div>`;
  if (pool.vdevs && pool.vdevs.length) {
    html += `<table>
      <thead><tr><th>VDev</th><th>State</th><th>Errors<br>R,W,Ck</th></tr></thead>
      <tbody>`;
    pool.vdevs.forEach((v) => {
      const totalErr = (v.read_errors || 0) + (v.write_errors || 0) + (v.cksum_errors || 0);
      const rowCls = totalErr > 0 ? 'row-amber' : '';
      const errCls = totalErr === 0 ? 'cell-green' : totalErr < 5 ? 'cell-amber' : 'cell-red';
      const vdevName = v.name.length > 12 ? '*' + esc(v.name.slice(-12)) : esc(v.name);
      const errors = `${v.read_errors}, ${v.write_errors}, ${v.cksum_errors}`;
      html += `<tr class="${rowCls}">
        <td title="${esc(v.name)}">${vdevName}</td>
        <td>${pill(v.state)}</td>
        <td class="${errCls}">${errors}</td>
      </tr>`;
    });
    html += `</tbody></table>`;
  }
  el.innerHTML = html;
}

function renderZFSARC(arc) {
  if (!arc) return;
  const el = document.getElementById('zfs-arc-content');
  const hitPct = arc.hit_rate != null ? (arc.hit_rate * 100).toFixed(1) : '—';
  const hitCls =
    arc.hit_rate >= 0.8 ? 'var(--green)' : arc.hit_rate >= 0.5 ? 'var(--amber)' : 'var(--red)';
  el.innerHTML = `<div class="arc-stats">
    <div class="arc-stat">
      <div class="arc-stat-label">Hit rate</div>
      <div class="arc-stat-val" style="color:${hitCls}">${hitPct}%</div>
    </div>
    <div class="arc-stat">
      <div class="arc-stat-label">ARC size</div>
      <div class="arc-stat-val">${fmtBytes(arc.size_bytes)}</div>
    </div>
    <div class="arc-stat">
      <div class="arc-stat-label">Max ARC</div>
      <div class="arc-stat-val">${fmtBytes(arc.max_bytes)}</div>
    </div>
  </div>`;
}

function renderZFSDatasets(datasets) {
  if (!datasets) return;
  const el = document.getElementById('zfs-datasets-content');
  let html = `<table>
    <thead><tr><th>Dataset</th><th>Used</th><th>Avail</th><th>Ref</th><th>Ratio</th><th>Algo</th></tr></thead>
    <tbody>`;
  const root = datasets.length ? datasets[0].name : '';
  datasets.forEach((d) => {
    const depth = d.name === root ? 0 : d.name.split('/').length - root.split('/').length;
    const indent = '\u00a0\u00a0'.repeat(depth); // nbsp indent per level
    const label =
      depth === 0 ? esc(d.name) : indent + '\u2514\u00a0' + esc(d.name.split('/').pop());
    html += `<tr>
      <td>${label}</td>
      <td>${fmtGB(d.used_bytes)}</td>
      <td>${fmtGB(d.avail_bytes)}</td>
      <td>${fmtGB(d.refer_bytes)}</td>
      <td>${d.compress_ratio ? d.compress_ratio.toFixed(2) + 'x' : '\u2014'}</td>
      <td>${esc(d.compression) || '\u2014'}</td>
    </tr>`;
  });
  html += `</tbody></table>`;
  el.innerHTML = html;
}

function renderZFSSnapshots(snaps, count, totalBytes) {
  if (!snaps) return;
  document.getElementById('zfs-snap-badge').textContent =
    `${count} snap${count !== 1 ? 's' : ''} · ${fmtBytes(totalBytes)}`;
  const el = document.getElementById('zfs-snapshots-content');
  let html = `<table>
    <thead><tr><th>Snapshot</th><th>Created</th><th>Size</th></tr></thead>
    <tbody>`;
  snaps.forEach((s) => {
    html += `<tr>
      <td>${esc(s.name)}</td>
      <td>${fmtTime(s.creation)}</td>
      <td>${fmtBytes(s.used_bytes)}</td>
    </tr>`;
  });
  html += `</tbody></table>`;
  el.innerHTML = html;
}

// ── Hardware section ───────────────────────────────────────────────────────

// tempHistory: { [diskId]: [{ts, celsius}] }
let tempHistory = {};

function renderDiskCards(disks) {
  if (!disks) return;
  // Sort by dev name for consistent ordering
  const sorted = [...disks].sort((a, b) =>
    (a.dev || a.by_id || '').localeCompare(b.dev || b.by_id || '')
  );

  const container = document.getElementById('disk-table');
  let html = `<div class="table-scroll">
    <table>
      <thead><tr>
        <th>Dev</th>
        <th>ID</th>
        <th>Health</th>
        <th>Temp</th>
        <th>Pwr-on</th>
        <th>Sectors<br>R,P,C</th>
      </tr></thead>
      <tbody>`;
  sorted.forEach((d) => {
    const dev = d.dev ? d.dev.replace('/dev/', '') : d.by_id || '—';
    const serial = d.by_id ? d.by_id.slice(-12) : d.serial || '—';
    const model = d.model || '—';
    const healthCls =
      d.health === 'PASSED' ? 'cell-green' : d.health === 'UNKNOWN' ? '' : 'cell-red';
    const tempCls = 'cell-' + (statusClass(d.celsius_status) || 'none');
    const reallocCls = 'cell-' + (statusClass(d.reallocated_status) || 'none');
    const pendingCls = 'cell-' + (statusClass(d.pending_status) || 'none');
    const uncorrCls = 'cell-' + (statusClass(d.uncorrectable_status) || 'none');
    const poh = d.power_on_hours != null ? Math.floor(d.power_on_hours / 24) + ' d' : '—';
    const sectorsCls =
      reallocCls !== 'cell-' ? reallocCls : pendingCls !== 'cell-' ? pendingCls : uncorrCls;
    const sectorsVal = `${d.reallocated_sectors ?? '—'}, ${d.pending_sectors ?? '—'}, ${d.uncorrectable_errors ?? '—'}`;
    html += `<tr>
        <td title="${esc(d.by_id)}">${esc(dev)}</td>
        <td class="cell-model" title="${esc(model)}">${esc(serial)}</td>
        <td class="${healthCls}">${esc(d.health) || '—'}</td>
        <td class="${tempCls}">${d.celsius != null ? d.celsius + ' °C' : '—'}</td>
        <td>${poh}</td>
        <td class="${sectorsCls}">${sectorsVal}</td>
      </tr>`;
  });
  html += `</tbody></table></div>`;
  container.innerHTML = html;

  // Assign colors based on sorted order so hues are evenly spaced across all disks
  diskColorMap = {};
  byIdToDevMap = {};
  const colors = diskColorsFor(sorted.length);
  sorted.forEach((d, i) => {
    if (d.by_id) {
      diskColorMap[d.by_id] = colors[i];
      byIdToDevMap[d.by_id] = (d.dev || d.by_id).replace('/dev/', '');
    }
  });
  applyDiskColors();
}

function applyTempHistory(rows) {
  if (!rows) return;
  rows.forEach((r) => {
    if (!tempHistory[r.disk]) tempHistory[r.disk] = [];
    tempHistory[r.disk].push({ ts: r.ts, celsius: r.celsius });
  });
  renderTempChart();
}

function appendTempPoint(disks) {
  if (!disks) return;
  const now = Math.floor(Date.now() / 1000);
  disks.forEach((d) => {
    if (d.celsius == null || !d.by_id) return;
    if (!tempHistory[d.by_id]) tempHistory[d.by_id] = [];
    tempHistory[d.by_id].push({ ts: now, celsius: d.celsius });
  });
  renderTempChart();
}

function renderTempChart() {
  if (!tempChart) return;
  const diskIds = Object.keys(tempHistory);
  if (!diskIds.length) return;

  // Assign stable colors keyed by by_id (fallback if renderDiskCards hasn't run)
  diskIds.forEach((id, i) => {
    if (!diskColorMap[id]) diskColorMap[id] = PALETTE[i % PALETTE.length];
  });

  const series = diskIds.map((id) => {
    const sorted = tempHistory[id].slice().sort((a, b) => a.ts - b.ts);
    const gapMs = smartPollIntervalS * 2.5 * 1000;
    const data = [];
    sorted.forEach((p, i) => {
      if (i > 0 && (p.ts - sorted[i - 1].ts) * 1000 > gapMs) {
        data.push([p.ts * 1000 - 1, null]); // break the line
      }
      data.push([p.ts * 1000, p.celsius]);
    });
    return {
      name: byIdToDevMap[id] || id.replace(/^.*\/([^/]+)$/, '$1'),
      type: 'line',
      showSymbol: false,
      connectNulls: false,
      emphasis: { disabled: true },
      color: diskColorMap[id],
      lineStyle: { opacity: 0.7 },
      data,
    };
  });

  const allPoints = Object.values(tempHistory).flat();
  const allTemps = allPoints.map((p) => p.celsius);
  const yMin = Math.min(...allTemps) - 1;
  const yMax = Math.max(...allTemps) + 1;

  const allTsMs = allPoints.map((p) => p.ts * 1000);
  const lastTs = Math.max(...allTsMs);
  const firstTs = Math.min(...allTsMs);
  const halfWindowMs = (tempHistoryHours / 2) * 3600 * 1000;
  const xMin = Math.min(firstTs, lastTs - halfWindowMs);

  tempChart.setOption(
    {
      backgroundColor: 'transparent',
      tooltip: {
        trigger: 'axis',
        formatter: (params) => {
          const time = new Date(params[0].axisValue).toLocaleTimeString();
          return (
            time + '<br>' + params.map((p) => `${p.seriesName}: ${p.value[1]} °C`).join('<br>')
          );
        },
      },
      legend: { show: false },
      grid: { left: 40, right: 10, top: 10, bottom: 38 },
      xAxis: {
        type: 'time',
        name: 'time [h]',
        nameLocation: 'middle',
        nameGap: 25,
        nameTextStyle: { fontSize: 12 },
        minInterval: 3600 * 1000,
        min: xMin,
        max: lastTs,
        axisLabel: { fontSize: 10, formatter: (val) => String(new Date(val).getHours()) },
      },
      yAxis: {
        type: 'value',
        name: '°C',
        nameTextStyle: { fontSize: 10 },
        axisLabel: { fontSize: 10 },
        min: yMin,
        max: yMax,
      },
      series,
    },
    true
  );

  applyDiskColors();
}

function applyDiskColors() {
  document.querySelectorAll('#disk-table tbody tr').forEach((row) => {
    const cell = row.querySelector('td:first-child');
    if (!cell) return;
    const color = diskColorMap[cell.title];
    if (color) {
      cell.style.color = oklchToRgba(color, 0.7);
      cell.style.fontWeight = '600';
    }
  });
}

function loadHardware() {
  fetch('/api/hardware')
    .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
    .then((data) => {
      if (data.poll_interval_s) smartPollIntervalS = data.poll_interval_s;
      if (data.temp_history_hours) tempHistoryHours = data.temp_history_hours;
      renderDiskCards(data.disks);
      setUpdated('hw-updated');
      tempHistory = {};
      applyTempHistory(data.history);
    })
    .catch((err) => console.error('Hardware load error:', err));
}

// ── SSE connection ─────────────────────────────────────────────────────────

function connectSSE() {
  const es = new EventSource('/api/events');

  es.onmessage = (evt) => {
    let msg;
    try {
      msg = JSON.parse(evt.data);
    } catch {
      return;
    }

    if (msg.type === 'init' || msg.type === 'smart') {
      if (msg.poll_interval_s) smartPollIntervalS = msg.poll_interval_s;
      if (msg.temp_history_hours) tempHistoryHours = msg.temp_history_hours;
      renderDiskCards(msg.disks);
      setUpdated('hw-updated');
      if (msg.type === 'init' && msg.history) {
        tempHistory = {};
        applyTempHistory(msg.history);
      } else {
        appendTempPoint(msg.disks);
      }
    } else if (msg.type === 'files') {
      loadFiles();
    } else if (msg.type === 'zfs') {
      loadZFS();
    }
  };

  es.onerror = () => {
    console.warn('SSE disconnected; will auto-reconnect');
  };
}

// ── Refresh button wiring ──────────────────────────────────────────────────

document.getElementById('refresh-files').addEventListener('click', loadFiles);
document.getElementById('refresh-zfs').addEventListener('click', loadZFS);
document.getElementById('refresh-hw').addEventListener('click', loadHardware);

// ── Initialise ─────────────────────────────────────────────────────────────

initMobileNav();
initMediumNav();
initCharts();
loadFiles();
loadZFS();
connectSSE();
