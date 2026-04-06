#!/usr/bin/env node
// Takes screenshots of each dashboard tab.
// Usage: node scripts/screenshot.js [http://localhost:8080]
// Output: docs/screenshots/{mobile-files,mobile-zfs,mobile-hardware,desktop}.png

'use strict';

const { chromium } = require('playwright');
const path = require('path');
const fs   = require('fs');

const BASE = process.argv[2] || 'http://localhost:8080';
const OUT  = path.resolve(__dirname, '..', 'docs', 'screenshots');
fs.mkdirSync(OUT, { recursive: true });

// Wait selectors: element that becomes non-empty once SSE data has arrived.
const READY = {
  zfs:      '#zfs-pool-content table',
  hardware: '#disk-table table',
  files:    '#chart-sunburst canvas',
};

const SHOTS = [
  { hash: 'files',    width: 430,  height: 800,  label: 'mobile-files'     },
  { hash: 'zfs',      width: 430,  height: 800,  label: 'mobile-zfs'       },
  { hash: 'hardware', width: 430,  height: 800,  label: 'mobile-hardware'  },
  { hash: 'files',    width: 1600, height: 880,  label: 'desktop'          },
];

(async () => {
  const browser = await chromium.launch();

  for (const shot of SHOTS) {
    const ctx  = await browser.newContext({ viewport: { width: shot.width, height: shot.height } });
    const page = await ctx.newPage();

    await page.goto(`${BASE}/#${shot.hash}`, { waitUntil: 'load' });

    // Wait for the data-populated element (SSE delivers data shortly after load).
    const selector = READY[shot.hash];
    if (selector) {
      try {
        await page.waitForSelector(selector, { timeout: 10000 });
      } catch {
        console.warn(`  [warn] timed out waiting for ${selector} on ${shot.hash}`);
      }
    }

    const file = path.join(OUT, `${shot.label}.png`);
    await page.screenshot({ path: file, fullPage: false });
    console.log(`  saved ${file}`);

    await ctx.close();
  }

  await browser.close();
})();
