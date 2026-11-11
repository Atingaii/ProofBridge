import assert from 'node:assert/strict';
import { chromium } from 'playwright';
const baseURL = process.env.SITE_LOADING_TEST_URL || 'http://127.0.0.1:37638';
const gate = () => { let release; const promise = new Promise(resolve => { release = resolve; }); return { promise, release }; };
const until = async (check, message) => { const deadline = Date.now() + 30000; while (!await check()) { assert.ok(Date.now() < deadline, message); await new Promise(resolve => setTimeout(resolve, 30)); } };
const browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 960 }, timezoneId: 'UTC' });
  page.setDefaultTimeout(30000); await page.clock.install();
  const requests = [], errors = [], holds = new Map([['30:first', gate()], ['30:empty', gate()], ['30:last', gate()]]);
  let failLast = true;
  page.on('pageerror', error => errors.push(error.message));
  await page.addInitScript(() => {
    localStorage.setItem('codexBridge.language', 'zh'); window.__aborts = [];
    const original = window.fetch.bind(window);
    window.fetch = (url, init) => { init?.signal?.addEventListener('abort', () => window.__aborts.push(String(url))); return original(url, { ...init, signal: undefined }); };
  });
  const increment = (id, count, hasMore, nextCursor, days = 30) => {
    const metric = { inputTokens: count * 10, outputTokens: 0, cacheTokens: 0, totalTokens: count * 10, callCount: count, estimatedCostUsd: count, costKnown: count > 0 };
    return { overview: { ...metric, days, timezoneOffset: 0, runs: count ? 1 : 0, machines: count ? 1 : 0, trend: count ? [{ ...metric, date: '2026-10-03' }] : [], items: count ? [{ ...metric, runId: id, title: `Run ${id}`, machineId: 'same', machineName: 'Shared machine', createdAt: 1, status: 'completed', byCli: [] }] : [] }, page: { scannedRuns: 1, hasMore, nextCursor } };
  };
  await page.route('**/api/**', async route => {
    assert.equal(route.request().method(), 'GET', 'overview loading cannot submit sync/model work');
    const url = new URL(route.request().url());
    if (url.pathname === '/api/me') return route.fulfill({ json: { user: { id: 'fixture', username: 'fixture' } } });
    if (url.pathname !== '/api/usage/overview') return route.fulfill({ json: {} });
    assert.equal(url.searchParams.get('limit'), '1');
    const days = Number(url.searchParams.get('days')), cursor = url.searchParams.get('cursor') || 'first', key = `${days}:${cursor}`;
    requests.push(key); const held = holds.get(key); if (held) await held.promise;
    let json;
    if (days === 30) {
      if (cursor === 'first') json = increment('A', 1, true, 'empty');
      else if (cursor === 'empty') json = increment('', 0, true, 'last');
      else if (failLast) return route.fulfill({ status: 504, json: {} });
      else json = increment('B', 2, false);
    } else if (days === 90 && cursor === 'first') json = increment('', 0, true, 'tail', days);
    else json = increment(`range${days}`, 4, false, undefined, days);
    await route.fulfill({ json }).catch(() => {});
  });
  const release = key => { const held = holds.get(key); holds.delete(key); held?.release(); };
  const count = key => requests.filter(item => item === key).length;
  const status = page.getByRole('status').filter({ hasText: '个对话' });
  const navigate = path => page.evaluate(next => { history.pushState({}, '', next); window.dispatchEvent(new PopStateEvent('popstate')); }, path);
  await page.goto(`${baseURL}/orchestrate/usage`, { waitUntil: 'domcontentloaded' });
  await until(() => count('30:first') === 1, 'first request missing');
  await page.clock.runFor(15000); release('30:first');
  await page.getByText('Run A', { exact: true }).waitFor();
  await until(() => count('30:empty') === 1, 'next page missing');
  assert.match(await status.innerText(), /统计中.*1 个对话.*部分结果/);
  await page.clock.runFor(15000); release('30:empty');
  await until(() => count('30:last') === 1, 'empty page did not advance');
  assert.match(await status.innerText(), /2 个对话/);
  assert.equal(await page.getByRole('alert').count(), 0, 'whole-chain elapsed >25sec must not expire healthy individual pages');
  release('30:last');
  await page.getByRole('alert').getByText('统计请求超时，请重试。').waitFor();
  await page.getByText('Run A', { exact: true }).waitFor();
  assert.match(await status.innerText(), /统计未完成.*2 个对话/);
  failLast = false; await page.getByRole('alert').getByRole('button', { name: '重试', exact: true }).click();
  await page.getByText('Run B', { exact: true }).waitFor();
  assert.match(await status.innerText(), /统计完成.*3 个对话/);
  assert.equal(count('30:first'), 1); assert.equal(count('30:empty'), 1); assert.equal(count('30:last'), 2);
  assert.equal(await page.getByText('Run A', { exact: true }).count(), 1);
  assert.equal(await page.getByText('Shared machine', { exact: true }).count(), 1);
  assert.equal(await page.locator('main section').filter({ has: page.getByText('30', { exact: true }) }).count() > 0, true, 'totals must add successful runs exactly once');

  // Range changes cancel an in-flight page and hide the previous accumulator.
  holds.set('90:tail', gate());
  await page.getByRole('button', { name: '近 90 天', exact: true }).click();
  await until(() => count('90:tail') === 1, 'empty first page did not continue');
  assert.match(await status.innerText(), /统计中.*1 个对话/);
  assert.equal(await page.getByText('Run A', { exact: true }).count(), 0);
  assert.equal(await page.getByText('暂未记录对话用量。', { exact: true }).count(), 0);
  await page.getByRole('button', { name: '近 7 天', exact: true }).click();
  await page.getByText('Run range7', { exact: true }).waitFor();
  await until(() => page.evaluate(() => window.__aborts.some(path => path.includes('days=90') && path.includes('cursor=tail'))), 'range switch did not cancel');
  release('90:tail'); await page.waitForTimeout(100);
  assert.equal(await page.getByText('Run range90', { exact: true }).count(), 0);

  // A single hung page times out; retry starts at that same page, never page 1.
  holds.set('90:tail', gate()); await page.getByRole('button', { name: '近 90 天', exact: true }).click();
  await until(() => count('90:tail') === 2, 'hung page missing');
  await page.clock.runFor(25100); await page.getByRole('alert').waitFor();
  assert.match(await status.innerText(), /统计未完成.*1 个对话/);
  release('90:tail'); await page.getByRole('alert').getByRole('button', { name: '重试', exact: true }).click();
  await page.getByText('Run range90', { exact: true }).waitFor();
  assert.equal(count('90:first'), 2); assert.equal(count('90:tail'), 3);
  assert.match(await status.innerText(), /统计完成.*2 个对话/);

  await page.evaluate(() => { window.__aborts = []; });
  holds.set('30:first', gate()); await page.getByRole('button', { name: '近 30 天', exact: true }).click();
  await until(() => count('30:first') === 2, 'leave fixture missing');
  await navigate('/help');
  await until(() => page.evaluate(() => window.__aborts.some(path => path.includes('days=30') && !path.includes('cursor='))), 'leaving did not cancel');
  release('30:first'); await page.waitForTimeout(100);
  assert.equal(count('30:empty'), 1, 'unmounted chain must not request another page');
  assert.deepEqual(errors, []);
  console.log('Progressive overview browser passed: partial first page, >25s chain, empty pages, same-cursor retry, deduplication, range/route cancellation, stale response isolation, per-page timeout and final completion.');
} finally { await browser.close(); }
