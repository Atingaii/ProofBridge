import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { chromium } from 'playwright';

// Every API and WebSocket is intercepted. No production data or model calls.
const baseURL = process.env.DESKTOP_FIXES_TEST_URL || 'http://127.0.0.1:37628';
const screenshotDir = process.env.DESKTOP_FIXES_SCREENSHOT_DIR;
if (screenshotDir) await mkdir(screenshotDir, { recursive: true });
const gate = () => { let release; const promise = new Promise(resolve => { release = resolve; }); return { promise, release }; };
const waitUntil = async (predicate, message = 'fixture did not converge') => {
  const deadline = Date.now() + 12000;
  while (!await predicate()) {
    assert.ok(Date.now() < deadline, message);
    await new Promise(resolve => setTimeout(resolve, 25));
  }
};
const browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 960 } });
  page.setDefaultTimeout(15000);
  const errors = [], requests = [];
  page.on('pageerror', error => errors.push(error.message));
  const makeRun = id => ({ id, title: `Fixture ${id}`, agentId: 'agent', mode: 'collaboration', prompt: 'fixture',
    workerPair: 'codex-codex', maxTurns: 0, status: 'completed', createdAt: 1, updatedAt: 1 });
  const stats = id => ({ runId: id, runtimeSeconds: 50, startedAt: 1, finishedAt: 51, inputTokens: 20,
    outputTokens: 5, totalTokens: 25, cacheTokens: 0, callCount: 1, costKnown: true, estimatedCostUsd: .1,
    byCLI: [], byWorker: [], rounds: [], tasks: [] });
  const overview = days => ({ days, machines: 1, runs: 1, totalTokens: 25, inputTokens: 20, outputTokens: 5,
    cacheTokens: 0, callCount: 1, costKnown: true, estimatedCostUsd: .1, trend: [],
    items: [{ ...makeRun(`range-${days}`), runId: `range-${days}`, title: `Range ${days} result`,
      machineId: 'fixture-machine', machineName: 'Fixture machine', totalTokens: 25, inputTokens: 20,
      outputTokens: 5, cacheTokens: 0, callCount: 1, costKnown: true, estimatedCostUsd: .1, byCLI: [] }] });
  let heldRange = 30, rangeGate = gate(), rangeError = 0, heldRun = '', runGate, runError = 0, syncGate;
  await page.addInitScript(() => {
    localStorage.setItem('codexBridge.language', 'zh');
    window.__statsAborts = [];
    const original = window.fetch.bind(window);
    window.fetch = (input, init) => {
      const path = String(input);
      if (path.includes('/api/usage/overview') || path.includes('/api/orchestrations/')) {
        init?.signal?.addEventListener('abort', () => window.__statsAborts.push(path));
        // Deliberately ignore cancellation to exercise stale-response guards.
        return original(input, { ...init, signal: undefined });
      }
      return original(input, init);
    };
  });
  const routeAPI = async route => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    requests.push(path + url.search);
    let json = {};
    if (path === '/api/me') json = { user: { id: 'fixture-user', username: 'fixture', createdAt: 1, features: [] } };
    else if (path === '/api/usage/overview') {
      const days = Number(url.searchParams.get('days'));
      if (days === heldRange) await rangeGate.promise;
      if (rangeError) return route.fulfill({ status: rangeError, json: { message: 'Fixture overview unavailable' } });
      json = { overview: overview(days), page: { hasMore: false, scannedRuns: 1 } };
    } else if (path.endsWith('/usage-sync')) {
      assert.equal(route.request().method(), 'POST');
      if (syncGate) await syncGate.promise;
      return route.fulfill({ status: 202, json: { status: 'syncing' } });
    } else if (path.startsWith('/api/orchestrations/')) {
      const id = path.split('/')[3];
      if (id === heldRun) await runGate.promise;
      if (runError) return route.fulfill({ status: runError, json: { message: 'Fixture run unavailable' } });
      json = path.endsWith('/stats') ? { stats: stats(id) } : { run: makeRun(id) };
    } else assert.equal(route.request().method(), 'GET', `unexpected mutation: ${path}`);
    await route.fulfill({ json });
  };
  await page.route('**/api/**', routeAPI);
  await page.routeWebSocket('**/ws/**', ws => ws.onMessage(() => {}));
  const navigate = async path => page.evaluate(next => {
    history.pushState({}, '', next); window.dispatchEvent(new PopStateEvent('popstate'));
  }, path);
  const screenshot = async name => { if (screenshotDir) await page.screenshot({ path: `${screenshotDir}/${name}.png`, fullPage: true }); };
  const result = days => page.getByText(`Range ${days} result`, { exact: true });
  const loading = page.getByRole('status', { name: '正在加载统计…', exact: true });
  const retry = () => page.getByRole('alert').getByRole('button', { name: '重试', exact: true }).click();

  await page.goto(`${baseURL}/orchestrate/usage`, { waitUntil: 'domcontentloaded' });
  await waitUntil(() => requests.some(path => path.includes('days=30')));
  await page.getByRole('button', { name: '近 7 天', exact: true }).click();
  await result(7).waitFor();
  heldRange = undefined; rangeGate.release();
  await page.waitForTimeout(100);
  assert.equal(await result(30).count(), 0, 'late range response must not replace the latest selection');
  assert.ok((await page.evaluate(() => window.__statsAborts)).some(path => path.includes('days=30')));
  await screenshot('stats-range-success');

  heldRange = 90; rangeGate = gate();
  await page.getByRole('button', { name: '近 90 天', exact: true }).click();
  await loading.waitFor();
  assert.equal(await result(7).count(), 0, 'previous range must disappear while the new range loads');
  rangeError = 503; heldRange = undefined; rangeGate.release();
  await page.getByRole('alert').getByText('Fixture overview unavailable', { exact: true }).waitFor();
  rangeError = 0; await retry(); await result(90).waitFor();

  // A transport that never settles still leaves loading after the browser budget.
  await page.clock.install(); heldRange = 0; rangeGate = gate();
  await page.getByRole('button', { name: '全部时间', exact: true }).click();
  await waitUntil(() => requests.some(path => path.includes('days=0')));
  await page.clock.fastForward(25_100);
  await page.getByRole('alert').getByText('统计请求超时，请重试。', { exact: true }).waitFor();
  assert.equal(await loading.count(), 0);
  heldRange = undefined; rangeGate.release();
  await retry(); await result(0).waitFor();
  await screenshot('stats-timeout-recovery');

  // A real Hub 504 receives the same localized and retryable timeout state.
  rangeError = 504;
  await page.getByRole('button', { name: '近 7 天', exact: true }).click();
  await page.getByRole('alert').getByText('统计请求超时，请重试。', { exact: true }).waitFor();
  rangeError = 0; await retry(); await result(7).waitFor();

  heldRun = 'run-a'; runGate = gate();
  await navigate('/orchestrate/stats?run=run-a');
  await loading.waitFor();
  await waitUntil(() => requests.includes('/api/orchestrations/run-a/stats'));
  await navigate('/orchestrate/stats/?run=run-b');
  await page.getByText('Fixture run-b', { exact: true }).waitFor();
  heldRun = ''; runGate.release();
  await page.waitForTimeout(100);
  assert.equal(await page.getByText('Fixture run-a', { exact: true }).count(), 0, 'late run must never replace the selected run');
  assert.ok((await page.evaluate(() => window.__statsAborts)).includes('/api/orchestrations/run-a/stats'));

  runError = 503;
  await page.getByRole('button', { name: '刷新', exact: true }).click();
  await page.getByRole('alert').getByText('Fixture run unavailable', { exact: true }).waitFor();
  runError = 0; await retry(); await page.getByText('Fixture run-b', { exact: true }).waitFor();

  // Successful sync schedules a refresh; leaving the page must clear its timer.
  await page.getByRole('button', { name: '同步用量账本', exact: true }).click();
  await page.getByRole('button', { name: '同步用量账本', exact: true }).waitFor();
  const reads = requests.filter(path => path === '/api/orchestrations/run-b/stats').length;
  await navigate('/orchestrate/usage'); await result(30).waitFor();
  await page.clock.fastForward(2_000);
  assert.equal(requests.filter(path => path === '/api/orchestrations/run-b/stats').length, reads);

  // Pending sync is also canceled when the run page unmounts.
  await navigate('/orchestrate/stats?run=run-b'); await page.getByText('Fixture run-b', { exact: true }).waitFor();
  syncGate = gate();
  const syncs = requests.filter(path => path.endsWith('/usage-sync')).length;
  await page.getByRole('button', { name: '同步用量账本', exact: true }).click();
  await waitUntil(() => requests.filter(path => path.endsWith('/usage-sync')).length > syncs);
  const beforeLeave = requests.filter(path => path.endsWith('/stats')).length;
  await navigate('/orchestrate/usage'); await result(30).waitFor();
  syncGate.release(); syncGate = undefined;
  await page.clock.fastForward(26_000);
  assert.equal(requests.filter(path => path.endsWith('/stats')).length, beforeLeave);
  assert.equal(await page.getByRole('alert').count(), 0);
  assert.ok((await page.evaluate(() => window.__statsAborts)).includes('/api/orchestrations/run-b/usage-sync'));
  // Optional remembered-run state must not blank either statistics entry route.
  const storagePage = await browser.newPage({ viewport: { width: 1440, height: 960 } });
  storagePage.setDefaultTimeout(15000);
  storagePage.on('pageerror', error => errors.push(error.message));
  await storagePage.addInitScript(() => {
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      get() { throw new DOMException('Fixture storage blocked', 'SecurityError'); },
    });
  });
  await storagePage.route('**/api/**', routeAPI);
  await storagePage.routeWebSocket('**/ws/**', ws => ws.onMessage(() => {}));
  for (const path of ['/orchestrate/usage', '/orchestrate/stats']) {
    await storagePage.goto(`${baseURL}${path}`, { waitUntil: 'domcontentloaded' });
    await storagePage.getByText('Range 30 result', { exact: true }).waitFor();
  }
  await storagePage.goto(`${baseURL}/orchestrate/stats?run=storage-run`, { waitUntil: 'domcontentloaded' });
  await storagePage.getByText('Fixture storage-run', { exact: true }).waitFor();
  await storagePage.close();
  assert.deepEqual(errors, []);
  console.log('Statistics browser: stale range/run responses, loading isolation, 503/504 retry, timeout recovery, cancellation, sync timer cleanup and blocked-storage routes passed.');
  await page.close();
} finally { await browser.close(); }
