import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { chromium } from 'playwright';

const baseURL = process.env.WORKER_USAGE_TEST_URL || 'http://127.0.0.1:4197';
const sample = (slot, input) => ({ cli: 'codex', workerSlot: slot, model: 'same-model', inputTokens: input, outputTokens: 20, cacheReadTokens: 50, cacheWriteTokens: 0, estimatedCostUsd: 0.1, costKnown: true, native: true, callCount: 1 });
const a = sample('codex-a', 100), b = sample('codex-b', 200);
const fallback = { runId: 'fixture', inputTokens: 300, outputTokens: 40, cacheReadTokens: 100, cacheWriteTokens: 0, cacheTokens: 100, totalTokens: 440, runtimeSeconds: 10, estimatedCostUsd: .2, costKnown: true, byCli: [], byWorker: [a, b], rounds: [{ round: 1, totalTokens: 440, byWorker: [a, b] }], tasks: [] };
const va = { ...sample('verifier:codex-a', 30), modelSource: 'worker-config' }, vb = { ...sample('verifier:codex-b', 40), modelSource: 'worker-config' };
fallback.byWorker.push(va, vb); fallback.rounds[0].byWorker.push(va, vb);
fallback.cacheTokens += 100; fallback.estimatedCostUsd += 0.2;
fallback.inputTokens += 70; fallback.outputTokens += 40; fallback.cacheReadTokens += 100; fallback.totalTokens += 210; fallback.rounds[0].totalTokens += 210;
const originalStats = process.env.WORKER_USAGE_STATS_FIXTURE ? JSON.parse(readFileSync(process.env.WORKER_USAGE_STATS_FIXTURE, 'utf8')).stats : fallback;
let stats = originalStats;
const browser = await chromium.launch({ headless: true, args: ['--disable-dev-shm-usage'] });
try {
  const page = await browser.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()); });
  await page.addInitScript(() => { localStorage.setItem('codexBridge.language', 'zh'); history.replaceState(null, '', '/orchestrate/stats?run=fixture'); });
  await page.route('**/api/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    const data = path === '/api/me' ? { user: { id: 'fixture', username: 'fixture', features: {} } } : path.endsWith('/stats') ? { stats } : { run: { id: 'fixture', title: '参与者用量验证', status: 'completed' } };
    await route.fulfill({ json: data });
  });
  for (const pair of ['codex-codex', 'claude-claude', 'claude-codex']) {
    stats = pair === 'codex-codex' ? originalStats : JSON.parse(JSON.stringify(fallback));
    if (pair !== 'codex-codex') {
      const remap = (item) => {
        if (pair === 'claude-claude') { item.cli = 'claude'; item.workerSlot = item.workerSlot.replace('codex', 'claude'); }
        else if (item.workerSlot.endsWith('codex-a')) { item.cli = 'claude'; item.workerSlot = item.workerSlot.replace('codex-a', 'claude'); }
        else { item.workerSlot = item.workerSlot.replace('codex-b', 'codex'); }
      };
      stats.byWorker.forEach(remap); stats.rounds.forEach(round => round.byWorker.forEach(remap));
    }
  for (const [width, height, name] of [[1280, 1000, 'desktop'], [390, 844, 'mobile']]) {
    await page.setViewportSize({ width, height });
    await page.goto(baseURL);
    const charts = page.getByTestId('round-model-usage');
    await charts.waitFor();
    await page.waitForFunction(() => document.querySelectorAll('[data-testid="round-model-usage"] .recharts-wrapper').length === 1);
    const bars = charts.getByTestId('round-model-bars'), lines = charts.getByTestId('round-model-lines');
    const barButton = charts.getByRole('button', { name: '柱状图', exact: true }), lineButton = charts.getByRole('button', { name: '折线图', exact: true });
    assert.equal(await barButton.getAttribute('aria-pressed'), 'true');
    assert.ok(await bars.locator('.recharts-bar-rectangle').count() > 0);
    assert.equal(await lines.count(), 0);
    const barBox = await bars.boundingBox();
    await lineButton.click();
    assert.equal(await lineButton.getAttribute('aria-pressed'), 'true');
    assert.equal(await barButton.getAttribute('aria-pressed'), 'false');
    assert.equal(await bars.count(), 0);
    assert.ok(await lines.locator('.recharts-line-dot').count() > 0, 'single-round model must have a visible point');
    const lineBox = await lines.boundingBox();
    assert.ok(Math.abs(barBox.width - lineBox.width) < 2, 'both views must use the same full width');
    assert.equal(await charts.locator('thead th').count(), 7);
    assert.equal(await charts.getByRole('button').count(), 2);
    assert.doesNotMatch(await charts.innerText(), /Codex A|Codex B|Claude A|Claude B|verifier/);
    const actual = await charts.locator('tbody tr').evaluateAll(rows => rows.map(row => [0, 1, 5].map(i => row.cells[i].textContent).join('')));
    if (!(pair === 'codex-codex' && process.env.WORKER_USAGE_STATS_FIXTURE)) assert.deepEqual(await charts.locator('tbody tr:first-child td').allTextContents(), ['#1', 'same-model', '370', '80', '200', '650', '$0.400000']);
    for (const label of ['输入 Token', '输出 Token', '缓存 Token']) assert.ok(await page.getByText(label, { exact: true }).count() >= 2);
    if (pair === 'codex-codex' && process.env.WORKER_USAGE_STATS_FIXTURE) {
      assert.equal(await lines.locator('.recharts-line-dot').count(), 4, 'historical points must match the four model/round rows');
      assert.deepEqual(actual, ['#1未报告模型200,859', '#1gpt-5.6-sol24,292,847', '#2gpt-5.6-sol26,810,219', '#3gpt-5.6-sol77,282,295']);
    } else assert.deepEqual(actual, ['#1same-model650']);
    const summary = page.getByTestId('model-usage-summary');
    await summary.waitFor();
    if (!(pair === 'codex-codex' && process.env.WORKER_USAGE_STATS_FIXTURE)) {
      assert.equal(await summary.getByTestId('model-total').count(), 1);
      assert.deepEqual(await summary.locator('strong').allTextContents(), ['650', '$0.400000']);
      assert.deepEqual(await summary.locator('dd').allTextContents(), ['370', '80', '200']);
    } else {
      assert.deepEqual(await summary.locator('strong').allTextContents(), ['200,859', '—', '128,385,361', '$130.675914']);
    }
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${name} page overflows horizontally`);
    assert.ok(await charts.locator('table').evaluate(table => table.parentElement.clientWidth <= innerWidth), `${name} table scroll region overflows`);
    if (process.env.WORKER_USAGE_SCREENSHOT_DIR) await charts.screenshot({ path: `${process.env.WORKER_USAGE_SCREENSHOT_DIR}/${pair}-${name}.png` });
  }
  }
  if (process.env.MODEL_TOTAL_STATS_FIXTURE) {
    stats = JSON.parse(readFileSync(process.env.MODEL_TOTAL_STATS_FIXTURE, 'utf8')).stats;
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 1000 }); await page.goto(baseURL);
      const summary = page.getByTestId('model-usage-summary'); await summary.waitFor();
      assert.deepEqual(await summary.locator('h3').allTextContents(), ['deepseek-v4-flash', 'gpt-5.6-terra']);
      assert.deepEqual(await summary.locator('strong').allTextContents(), ['377,533', '$0.013575', '354,882', '$0.266411']);
      assert.deepEqual(await summary.locator('dd').allTextContents(), ['13,357', '17,552', '346,624', '31,888', '11,698', '311,296']);
      assert.equal(await page.getByTestId('round-model-usage').locator('tbody tr').count(), 8);
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      if (process.env.WORKER_USAGE_SCREENSHOT_DIR) await summary.screenshot({ path: `${process.env.WORKER_USAGE_SCREENSHOT_DIR}/model-totals-${width}.png` });
    }
  }
  // Many rounds retain readable spacing and scrolling in either selected view.
  stats = { ...fallback, rounds: Array.from({ length: 40 }, (_, i) => ({ round: i + 1, totalTokens: 170, byWorker: [a] })) };
  await page.goto(baseURL);
  const many = page.getByTestId('round-model-usage');
  await many.waitFor();
  for (const label of ['柱状图', '折线图', '柱状图']) {
    const button = many.getByRole('button', { name: label, exact: true });
    await button.focus(); await page.keyboard.press('Enter');
    assert.equal(await button.getAttribute('aria-pressed'), 'true');
    assert.equal(await many.locator('.recharts-wrapper').count(), 1);
    const id = label === '柱状图' ? 'round-model-bars' : 'round-model-lines';
    const viewport = many.getByTestId(id).locator('.overflow-x-auto');
    assert.ok(await viewport.evaluate(el => el.scrollWidth > el.clientWidth), 'long chart must scroll within its own viewport');
    await viewport.evaluate(el => { el.scrollLeft = el.scrollWidth; });
    assert.ok(await viewport.evaluate(el => el.scrollLeft > 0));
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
  }
  // Completed legacy runs and selected tasks use the same summary projection.
  stats = { ...fallback, tasks: [{ ...fallback, taskNumber: 2, rounds: [{ round: 2, totalTokens: 90 }], totalTokens: 90 }] };
  await page.goto(baseURL);
  await page.getByRole('button', { name: '任务 2', exact: true }).click();
  assert.deepEqual(await page.getByTestId('round-model-usage').locator('tbody tr').evaluateAll(rows => rows.map(row => [0, 1, 5].map(i => row.cells[i].textContent).join(''))), ['#2未报告模型90']);
  assert.deepEqual(await page.getByTestId('model-usage-summary').locator('strong').allTextContents(), ['90', '—']);
  await page.getByRole('button', { name: '全部任务', exact: true }).click();
  assert.deepEqual(await page.getByTestId('model-usage-summary').locator('strong').allTextContents(), ['650', '$0.400000']);
  assert.deepEqual(await page.getByTestId('round-model-usage').locator('tbody tr').evaluateAll(rows => rows.map(row => [0, 1, 5].map(i => row.cells[i].textContent).join(''))), ['#1same-model650']);
  for (const [status, text] of [['upgrade-required', '请更新运行 CLI'], ['offline', '运行 CLI 的机器离线'], ['pending', '正在等待历史型号补查'], ['unresolved', '本地扫描尚未找到唯一匹配']]) {
    stats = { ...fallback, modelRecoveryStatus: status };
    await page.goto(baseURL);
    await page.getByTestId('round-model-usage').waitFor();
    assert.match(await page.getByRole('status').innerText(), new RegExp(text));
  }
  assert.deepEqual(errors, []);
  console.log('Round/model charts: three CLI pairs, historical totals, task scope, desktop/mobile, exact table and console passed.');
} finally { await browser.close(); }
