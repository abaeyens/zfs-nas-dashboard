/* NAS Dashboard — app.js
 * Three sections: Files, ZFS, Hardware.
 * Data sources:
 *   GET /api/files    → FilesResult  { tree, users }
 *   GET /api/zfs      → ZFSResult    { pool, datasets, snapshots, arc, ... }
 *   SSE /api/events   → { type:"init"|"smart", disks, history? }
 */

'use strict';

// ── Utilities ──────────────────────────────────────────────────────────────

function fmtBytes(n) {
  if (n == null) return '—';
  const u = ['B','KB','MB','GB','TB'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return n.toFixed(i === 0 ? 0 : 1) + ' ' + u[i];
}

function fmtGB(n) {
  if (n == null) return '—';
  return Math.round(n / (1024 ** 3)) + ' GB';
}

function fmtTime(isoOrUnix) {
  if (!isoOrUnix) return '—';
  const n = Number(isoOrUnix);
  const d = !isNaN(n) ? new Date(n * 1000) : new Date(isoOrUnix);
  if (isNaN(d)) return '—';
  const pad = v => String(v).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())} `
       + `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function pill(status) {
  status = (status || 'unknown').toLowerCase();
  const cls = status === 'green' ? 'pill-green'
            : status === 'amber' ? 'pill-amber'
            : status === 'red'   ? 'pill-red'
            : status === 'online'    ? 'pill-online'
            : status === 'degraded'  ? 'pill-degraded'
            : status === 'faulted'   ? 'pill-faulted'
            : 'pill-amber';
  return `<span class="pill ${cls}">${status.toUpperCase()}</span>`;
}

function statusClass(s) {
  return (s || '').toLowerCase();   // "green" | "amber" | "red"
}

function setUpdated(id) {
  const el = document.getElementById(id);
  if (el) el.textContent = 'Updated ' + new Date().toLocaleTimeString();
}

// ── Mobile nav ─────────────────────────────────────────────────────────────

function initMobileNav() {
  const buttons = document.querySelectorAll('.nav-btn');
  buttons.forEach(btn => {
    btn.addEventListener('click', () => {
      buttons.forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
      const target = btn.dataset.target;
      document.querySelectorAll('.panel').forEach(p => {
        p.classList.toggle('active', p.dataset.name === target);
      });
    });
  });
  // Activate first panel on mobile on load
  const firstPanel = document.querySelector('.panel');
  if (firstPanel) firstPanel.classList.add('active');
}

// ── ECharts helpers ────────────────────────────────────────────────────────

let sunburstChart = null;
let userPieChart  = null;
let tempChart     = null;
const DISK_COLORS = ['#5470c6','#91cc75','#fac858','#ee6666','#73c0de','#3ba272','#fc8452','#9a60b4','#ea7ccc'];
let diskColorMap  = {}; // by_id → color

function initCharts() {
  sunburstChart = echarts.init(document.getElementById('chart-sunburst'), 'dark');
  userPieChart  = echarts.init(document.getElementById('chart-userpie'),  'dark');
  tempChart     = echarts.init(document.getElementById('chart-temps'),    'dark');

  const ro = new ResizeObserver(() => {
    sunburstChart && sunburstChart.resize();
    userPieChart  && userPieChart.resize();
    tempChart     && tempChart.resize();
  });
  document.querySelectorAll('.chart').forEach(el => ro.observe(el));
}

// ── Files section ──────────────────────────────────────────────────────────

function loadFiles() {
  document.getElementById('files-scanning').hidden = false;
  fetch('/api/files')
    .then(r => r.ok ? r.json() : Promise.reject(r.status))
    .then(data => {
      document.getElementById('files-scanning').hidden = true;
      renderSunburst(data);
      renderUserPie(data);
      setUpdated('files-updated');
    })
    .catch(err => {
      document.getElementById('files-scanning').hidden = false;
      document.getElementById('files-scanning').textContent = 'Error loading files: ' + err;
    });
}

function treeToSunburst(node, colorIndex) {
  if (!node) return null;
  const out = {
    name: node.name || node.path || '/',
    value: node.size_bytes,
    itemStyle: {},
  };
  if (colorIndex !== undefined) {
    out.itemStyle.color = DISK_COLORS[colorIndex % DISK_COLORS.length];
  }
  if (node.children && node.children.length) {
    out.children = node.children.map((c, i) =>
      treeToSunburst(c, colorIndex !== undefined ? colorIndex : i)
    ).filter(Boolean);
  }
  return out;
}

function renderSunburst(filesData) {
  const tree = filesData && filesData.tree;
  if (!tree || !sunburstChart) return;
  const root = treeToSunburst(tree);
  if (!root) return;

  // Assign distinct colors to top-level directory children
  if (root.children) {
    root.children.forEach((c, i) => {
      c.itemStyle = { color: DISK_COLORS[i % DISK_COLORS.length] };
    });
  }

  // Append "Snapshots" and "Available" segments so the donut shows full pool capacity
  const snapshotBytes = (filesData && filesData.snapshot_bytes) || 0;
  const availBytes = (filesData && filesData.avail_bytes) || 0;
  if (snapshotBytes > 0) {
    if (!root.children) root.children = [];
    root.children.push({ name: 'Snapshots & Trashed', value: snapshotBytes, itemStyle: { color: '#8c8c8c' } });
    root.value = (root.value || 0) + snapshotBytes;
  }
  if (availBytes > 0) {
    if (!root.children) root.children = [];
    root.children.push({ name: 'Available', value: availBytes, itemStyle: { color: '#2d4a2d' } });
    root.value = (root.value || 0) + availBytes;
  }

  sunburstChart.setOption({
    backgroundColor: 'transparent',
    tooltip: {
      trigger: 'item',
      formatter: p => `${p.name}<br/>${fmtBytes(p.value)}`,
    },
    series: [{
      type: 'sunburst',
      data: root.children || [root],
      radius: ['15%', '95%'],
      label: { show: false },
      emphasis: { focus: 'ancestor' },
      levels: [{}, { r0: '15%', r: '35%' }, { r0: '35%', r: '60%' }, { r0: '60%', r: '80%' }, { r0: '80%', r: '95%' }],
    }],
  });
}

function renderUserPie(filesData) {
  const users = filesData && filesData.users;
  if (!users || !userPieChart) return;
  const items = users.map(u => ({ name: u.user, value: u.size_bytes }));
  userPieChart.setOption({
    backgroundColor: 'transparent',
    tooltip: {
      trigger: 'item',
      formatter: p => `${p.name}<br/>${fmtBytes(p.value)} (${p.percent.toFixed(1)}%)`,
    },
    legend: { show: false },
    series: [{
      type: 'pie',
      data: items,
      radius: ['35%', '70%'],
      label: { fontSize: 11 },
      emphasis: { itemStyle: { shadowBlur: 6 } },
    }],
  });
}

// ── ZFS section ────────────────────────────────────────────────────────────

function loadZFS() {
  fetch('/api/zfs')
    .then(r => r.ok ? r.json() : Promise.reject(r.status))
    .then(data => {
      renderZFSPool(data.pool);
      renderZFSARC(data.arc);
      renderZFSDatasets(data.datasets);
      renderZFSSnapshots(data.snapshots, data.snapshot_count, data.snapshot_total_bytes);
      setUpdated('zfs-updated');
    })
    .catch(err => console.error('ZFS load error:', err));
}

function renderZFSPool(pool) {
  if (!pool) return;
  const el = document.getElementById('zfs-pool-content');
  const stateCls = pool.state ? pool.state.toLowerCase() : 'unknown';
  let html = `<div class="pool-meta">
    <span class="state">${pool.name}</span>
    ${pill(pool.state)}
    <span style="color:var(--muted);font-size:12px">${(() => {
      if (!pool.scan || !pool.scan.type) return '';
      let s = pool.scan.type + ': ' + pool.scan.state;
      if (pool.scan.end_time) {
        const d = new Date(pool.scan.end_time);
        if (!isNaN(d)) {
          const pad = v => String(v).padStart(2, '0');
          s += ' (' + d.getFullYear() + '-' + pad(d.getMonth()+1) + '-' + pad(d.getDate()) + ')';
        }
      }
      return s;
    })()}</span>
  </div>`;
  if (pool.vdevs && pool.vdevs.length) {
    html += `<table>
      <thead><tr><th>VDev</th><th>State</th><th>Errors<br>R,W,Ck</th></tr></thead>
      <tbody>`;
    pool.vdevs.forEach(v => {
      const hasErr = v.read_errors || v.write_errors || v.cksum_errors;
      const rowCls = hasErr > 0 ? 'row-amber' : '';
      const vdevName = v.name.length > 12 ? '*' + v.name.slice(-12) : v.name;
      const errors = `${v.read_errors}, ${v.write_errors}, ${v.cksum_errors}`;
      html += `<tr class="${rowCls}">
        <td title="${v.name}">${vdevName}</td>
        <td>${pill(v.state)}</td>
        <td>${errors}</td>
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
  const hitCls = arc.hit_rate >= 0.8 ? 'var(--green)' : arc.hit_rate >= 0.5 ? 'var(--amber)' : 'var(--red)';
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
      <div class="arc-stat-val">${fmtBytes(arc.total_ram_bytes)}</div>
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
  datasets.forEach(d => {
    const depth = d.name === root ? 0 : (d.name.split('/').length - root.split('/').length);
    const indent = '\u00a0\u00a0\u00a0\u00a0'.repeat(depth);  // nbsp indent per level
    const label = depth === 0 ? d.name : indent + '\u2514\u00a0' + d.name.split('/').pop();
    html += `<tr>
      <td>${label}</td>
      <td>${fmtGB(d.used_bytes)}</td>
      <td>${fmtGB(d.avail_bytes)}</td>
      <td>${fmtGB(d.refer_bytes)}</td>
      <td>${d.compress_ratio ? d.compress_ratio.toFixed(2) + 'x' : '\u2014'}</td>
      <td>${d.compression || '\u2014'}</td>
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
  snaps.forEach(s => {
    html += `<tr>
      <td>${s.name}</td>
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
  disks.forEach(d => {
    const dev = d.dev ? d.dev.replace('/dev/', '') : (d.by_id || '—');
    const serial = d.by_id ? d.by_id.slice(-12) : (d.serial || '—');
    const model = d.model || '—';
    const healthCls = d.health === 'PASSED' ? 'cell-green' : d.health === 'UNKNOWN' ? '' : 'cell-red';
    const tempCls   = 'cell-' + (statusClass(d.celsius_status) || 'none');
    const reallocCls = 'cell-' + (statusClass(d.reallocated_status) || 'none');
    const pendingCls = 'cell-' + (statusClass(d.pending_status) || 'none');
    const uncorrCls  = 'cell-' + (statusClass(d.uncorrectable_status) || 'none');
    const poh = d.power_on_hours != null ? Math.floor(d.power_on_hours / 24) + ' d' : '—';
    const sectorsCls = reallocCls !== 'cell-' ? reallocCls : pendingCls !== 'cell-' ? pendingCls : uncorrCls;
    const sectorsVal = `${d.reallocated_sectors ?? '—'}, ${d.pending_sectors ?? '—'}, ${d.uncorrectable_errors ?? '—'}`;
    html += `<tr>
        <td title="${d.by_id}">${dev}</td>
        <td class="cell-model" title="${model}">${serial}</td>
        <td class="${healthCls}">${d.health || '—'}</td>
        <td class="${tempCls}">${d.celsius != null ? d.celsius + ' °C' : '—'}</td>
        <td>${poh}</td>
        <td class="${sectorsCls}">${sectorsVal}</td>
      </tr>`;
  });
  html += `</tbody></table></div>`;
  container.innerHTML = html;

  // Pre-assign colors so the device column is colored immediately,
  // before the first renderTempChart() populates diskColorMap.
  let colorIdx = Object.keys(diskColorMap).length;
  disks.forEach(d => {
    if (d.by_id && !diskColorMap[d.by_id]) {
      diskColorMap[d.by_id] = DISK_COLORS[colorIdx % DISK_COLORS.length];
      colorIdx++;
    }
  });
  applyDiskColors();
}

function applyTempHistory(rows) {
  if (!rows) return;
  rows.forEach(r => {
    if (!tempHistory[r.disk]) tempHistory[r.disk] = [];
    tempHistory[r.disk].push({ ts: r.ts, celsius: r.celsius });
  });
  renderTempChart();
}

function appendTempPoint(disks) {
  if (!disks) return;
  const now = Math.floor(Date.now() / 1000);
  disks.forEach(d => {
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

  // Assign stable colors keyed by by_id
  diskIds.forEach((id, i) => {
    if (!diskColorMap[id]) diskColorMap[id] = DISK_COLORS[i % DISK_COLORS.length];
  });

  const series = diskIds.map(id => {
    const points = tempHistory[id];
    return {
      name: id.replace(/^.*\/([^/]+)$/, '$1'),   // last path component
      type: 'line',
      showSymbol: false,
      color: diskColorMap[id],
      lineStyle: { opacity: 0.7 },
      data: points.map(p => [p.ts * 1000, p.celsius]),
    };
  });

  tempChart.setOption({
    backgroundColor: 'transparent',
    tooltip: { trigger: 'axis', formatter: params => {
      const time = new Date(params[0].axisValue).toLocaleTimeString();
      return time + '<br>' + params.map(p => `${p.seriesName}: ${p.value[1]} °C`).join('<br>');
    }},
    legend: { show: false },
    grid: { left: 40, right: 10, top: 10, bottom: 30 },
    xAxis: { type: 'time', axisLabel: { fontSize: 10 } },
    yAxis: { type: 'value', name: '°C', nameTextStyle: { fontSize: 10 }, axisLabel: { fontSize: 10 } },
    series,
  }, true);

  applyDiskColors();
}

function applyDiskColors() {
  document.querySelectorAll('#disk-table tbody tr').forEach(row => {
    const cell = row.querySelector('td:first-child');
    if (!cell) return;
    const color = diskColorMap[cell.title];
    if (color) { cell.style.color = color; cell.style.fontWeight = '600'; }
  });
}

// ── SSE connection ─────────────────────────────────────────────────────────

function connectSSE() {
  const es = new EventSource('/api/events');

  es.onmessage = evt => {
    let msg;
    try { msg = JSON.parse(evt.data); } catch { return; }

    if (msg.type === 'init' || msg.type === 'smart') {
      renderDiskCards(msg.disks);
      setUpdated('hw-updated');
      if (msg.type === 'init' && msg.history) {
        tempHistory = {};
        applyTempHistory(msg.history);
      } else {
        appendTempPoint(msg.disks);
      }
    }
  };

  es.onerror = () => {
    console.warn('SSE disconnected; will auto-reconnect');
  };
}

// ── Refresh button wiring ──────────────────────────────────────────────────

document.getElementById('refresh-files').addEventListener('click', loadFiles);
document.getElementById('refresh-zfs').addEventListener('click', loadZFS);

// ── Initialise ─────────────────────────────────────────────────────────────

initMobileNav();
initCharts();
loadFiles();
loadZFS();
connectSSE();

