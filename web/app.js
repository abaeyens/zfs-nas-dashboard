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

function fmtTime(isoOrUnix) {
  if (!isoOrUnix) return '—';
  const d = typeof isoOrUnix === 'number' ? new Date(isoOrUnix * 1000) : new Date(isoOrUnix);
  return d.toLocaleString();
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
      renderSunburst(data.tree);
      renderUserPie(data.users);
      setUpdated('files-updated');
    })
    .catch(err => {
      document.getElementById('files-scanning').hidden = false;
      document.getElementById('files-scanning').textContent = 'Error loading files: ' + err;
    });
}

function treeToSunburst(node) {
  if (!node) return null;
  const out = {
    name: node.name || node.path || '/',
    value: node.size_bytes,
    itemStyle: {},
  };
  if (node.children && node.children.length) {
    out.children = node.children.map(treeToSunburst).filter(Boolean);
  }
  return out;
}

function renderSunburst(tree) {
  if (!tree || !sunburstChart) return;
  const data = treeToSunburst(tree);
  sunburstChart.setOption({
    backgroundColor: 'transparent',
    tooltip: {
      trigger: 'item',
      formatter: p => `${p.name}<br/>${fmtBytes(p.value)}`,
    },
    series: [{
      type: 'sunburst',
      data: data ? [data] : [],
      radius: ['15%', '95%'],
      label: { show: true, fontSize: 10, overflow: 'truncate' },
      emphasis: { focus: 'ancestor' },
      levels: [{}, { r0: '15%', r: '35%' }, { r0: '35%', r: '60%' }, { r0: '60%', r: '80%' }, { r0: '80%', r: '95%' }],
    }],
  });
}

function renderUserPie(users) {
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
    <span style="color:var(--muted);font-size:12px">${pool.scan && pool.scan.type ? pool.scan.type + ': ' + pool.scan.state : ''}</span>
  </div>`;
  if (pool.vdevs && pool.vdevs.length) {
    html += `<table>
      <thead><tr><th>VDev</th><th>State</th><th>R err</th><th>W err</th><th>Ck err</th></tr></thead>
      <tbody>`;
    pool.vdevs.forEach(v => {
      const hasErr = v.read_errors || v.write_errors || v.cksum_errors;
      const rowCls = hasErr > 0 ? 'row-amber' : '';
      html += `<tr class="${rowCls}">
        <td>${v.name}</td>
        <td>${pill(v.state)}</td>
        <td>${v.read_errors}</td>
        <td>${v.write_errors}</td>
        <td>${v.cksum_errors}</td>
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
  datasets.forEach(d => {
    html += `<tr>
      <td>${d.name}</td>
      <td>${fmtBytes(d.used_bytes)}</td>
      <td>${fmtBytes(d.avail_bytes)}</td>
      <td>${fmtBytes(d.refer_bytes)}</td>
      <td>${d.compress_ratio ? d.compress_ratio.toFixed(2) + 'x' : '—'}</td>
      <td>${d.compression || '—'}</td>
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
  const container = document.getElementById('disk-cards');
  container.innerHTML = disks.map(d => `
    <div class="disk-card">
      <div class="disk-name" title="${d.by_id}">${d.dev || d.by_id}</div>
      <div class="disk-model" title="${d.serial}">${d.model || '—'}</div>
      <div class="disk-row">
        <span class="disk-label">Health</span>
        <span class="disk-val ${d.health === 'PASSED' ? 'green' : 'red'}">${d.health || '—'}</span>
      </div>
      <div class="disk-row">
        <span class="disk-label">Temp</span>
        <span class="disk-val ${statusClass(d.celsius_status)}">${d.celsius != null ? d.celsius + ' °C' : '—'}</span>
      </div>
      <div class="disk-row">
        <span class="disk-label">Pwr-on</span>
        <span class="disk-val">${d.power_on_hours != null ? Math.floor(d.power_on_hours / 24) + ' d' : '—'}</span>
      </div>
      <div class="disk-row">
        <span class="disk-label">Realloc</span>
        <span class="disk-val ${statusClass(d.reallocated_status)}">${d.reallocated_sectors ?? '—'}</span>
      </div>
      <div class="disk-row">
        <span class="disk-label">Pending</span>
        <span class="disk-val ${statusClass(d.pending_status)}">${d.pending_sectors ?? '—'}</span>
      </div>
      <div class="disk-row">
        <span class="disk-label">Uncorr</span>
        <span class="disk-val ${statusClass(d.uncorrectable_status)}">${d.uncorrectable_errors ?? '—'}</span>
      </div>
    </div>
  `).join('');
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

  const series = diskIds.map(id => {
    const points = tempHistory[id];
    return {
      name: id.replace(/^.*\/([^/]+)$/, '$1'),   // last path component
      type: 'line',
      showSymbol: false,
      data: points.map(p => [p.ts * 1000, p.celsius]),
    };
  });

  tempChart.setOption({
    backgroundColor: 'transparent',
    tooltip: { trigger: 'axis', formatter: params => {
      const time = new Date(params[0].axisValue).toLocaleTimeString();
      return time + '<br>' + params.map(p => `${p.seriesName}: ${p.value[1]} °C`).join('<br>');
    }},
    legend: { show: diskIds.length > 1, textStyle: { fontSize: 10 }, bottom: 0 },
    grid: { left: 40, right: 10, top: 10, bottom: diskIds.length > 1 ? 36 : 10 },
    xAxis: { type: 'time', axisLabel: { fontSize: 10 } },
    yAxis: { type: 'value', name: '°C', nameTextStyle: { fontSize: 10 }, axisLabel: { fontSize: 10 } },
    series,
  }, true);
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

