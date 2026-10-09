import assert from 'node:assert/strict';
import { chromium } from 'playwright';

// A local Vite server supplies assets only. Every API and WebSocket is mocked.
const baseURL = process.env.SITE_LOADING_TEST_URL || 'http://127.0.0.1:37638';
const gate = () => { let release; const promise = new Promise(resolve => { release = resolve; }); return { promise, release }; };
const until = async (check, message = 'fixture did not converge') => {
  const deadline = Date.now() + 20000;
  while (!await check()) { assert.ok(Date.now() < deadline, message); await new Promise(resolve => setTimeout(resolve, 30)); }
};
const browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 960 }, timezoneId: 'UTC' });
  page.setDefaultTimeout(25000);
  const errors = [], requests = [], holds = new Map(), failures = new Map();
  const startupHistory = gate();
  let startupHistoryReads = 0;
  page.on('pageerror', error => errors.push(error.message));
  const runs = ['a', 'b', 'active', 'fail'].map(id => ({ id, agentId: 'agent', title: `Fixture ${id}`, prompt: `Initial ${id}`,
    mode: 'collaboration', workerPair: 'codex-codex', maxTurns: 0, status: id === 'active' ? 'running' : 'completed', createdAt: 1, updatedAt: 1 }));
  const agent = { id: 'agent', name: 'Fixture machine', online: true, lastSeenAt: 1, workingDirs: ['/tmp/fixture'], capabilities: {
    approvalPolicy: 'never', sandbox: 'danger-full-access', orchestration: { codex: { available: true }, claude: { available: true } } } };
  const metric = { totalTokens: 10, inputTokens: 8, outputTokens: 2, cacheTokens: 0, callCount: 1, costKnown: true, estimatedCostUsd: .1 };
  const usage = id => ({ userId: id, username: `User ${id}`, ...metric, lastActiveAt: 1, activityStatus: 'active', onlineAgents: 1, totalAgents: 1, chatSessions: 1, orchestrationRuns: 1, runningRuns: 0 });
  await page.addInitScript(() => {
    localStorage.setItem('codexBridge.language', 'zh');
    window.__aborts = [];
    const fetch = window.fetch.bind(window);
    window.fetch = (url, init) => {
      init?.signal?.addEventListener('abort', () => window.__aborts.push(String(url)));
      // Keep the old response alive even after abort: UI guards must still work.
      return fetch(url, { ...init, signal: undefined });
    };
  });
  await page.route('**/api/**', async route => {
    assert.equal(route.request().method(), 'GET', 'performance checks must never write');
    const url = new URL(route.request().url()), path = url.pathname;
    const key = path.startsWith('/api/admin/') ? path + url.search : path;
    requests.push(key);
    const held = holds.get(key); if (held) await held.promise;
    if (failures.has(key)) return route.fulfill({ status: failures.get(key), json: { message: 'Fixture unavailable' } });
    let json = {};
    if (path === '/api/me') json = { user: { id: 'fixture', username: 'fixture', isAdmin: true, features: ['orchestration-plan-workspace'] } };
    else if (path === '/api/agents') json = { agents: [agent] };
    else if (path === '/api/orchestrations') {
      // A separate list effect can render clickable rows while bootstrap still
      // waits for its older list request. Its late cleanup must not clear a click.
      if (++startupHistoryReads === 1) await startupHistory.promise;
      json = { runs };
    }
    else if (/\/orchestrations\/[^/]+$/.test(path)) json = { run: runs.find(run => run.id === path.split('/')[3]) };
    else if (path.endsWith('/events')) {
      const id = path.split('/')[3];
      json = { events: [{ id: `${id}-end`, runId: id, seq: 1, kind: 'turn.end', source: 'codex', cli: 'codex', content: `History ${id}`, createdAt: 1,
        task: { graphId: 'graph', nodeId: 'node', taskNumber: 1 } }], hasMoreBefore: false };
    } else if (path.endsWith('/progress')) json = { graph: null, graphs: [], tasks: [], planItems: [], plan: null };
    else if (path === '/api/admin/usage') json = { overview: { ...metric, users: 1, activeUsers: 1, onlineUsers: 1, onlineAgents: 1, totalAgents: 1, chatSessions: 1, trend: [], items: [usage(`range-${url.searchParams.get('days')}`)] } };
    else if (/\/admin\/users\/[^/]+\/usage$/.test(path)) {
      const id = path.split('/')[4]; json = { detail: { ...metric, username: `User ${id}`, conversations: ['one', 'two'].map(name => ({ ...metric, id: name, kind: 'chat', title: `Conversation ${name}`, agentName: 'Fixture machine', updatedAt: 1, activityCount: 1, status: 'completed' })) } };
    } else if (path.includes('/conversations/')) {
      const id = path.split('/').at(-1); json = { conversation: { prompt: `Content ${id}`, items: [] } };
    }
    await route.fulfill({ json }).catch(() => {});
  });
  await page.routeWebSocket('**/ws/**', ws => ws.onMessage(() => {}));
  const count = path => requests.filter(item => item === path).length;
  const aborted = path => page.evaluate(key => window.__aborts.includes(key), path);
  const release = key => { const held = holds.get(key); holds.delete(key); held?.release(); };
  const hold = key => { holds.set(key, gate()); };
  const navigate = path => page.evaluate(next => { history.pushState({}, '', next); window.dispatchEvent(new PopStateEvent('popstate')); }, path);
  const select = id => page.locator(`[data-run-id="${id}"]`).getByRole('button', { name: `Fixture ${id}`, exact: true }).click();
  const loading = page.getByRole('status', { name: '正在加载对话…', exact: true });

  hold('/api/orchestrations/a'); hold('/api/orchestrations/a/events');
  await page.goto(`${baseURL}/orchestrate`, { waitUntil: 'domcontentloaded' });
  await select('a'); await loading.waitFor();
  assert.equal(await page.locator('aside textarea:visible').count(), 0, 'loading must not expose the new-run composer');
  assert.equal(await page.locator('aside').last().getByRole('button').count(), 0, 'loading must not expose default setup or send controls');
  assert.equal(await page.locator('#orchestration-live-input:visible').count(), 0);
  await until(() => count('/api/orchestrations/a') === 1);
  startupHistory.release();
  await page.waitForTimeout(150);
  assert.equal(new URL(page.url()).pathname, '/orchestrate/runs/a', 'late bootstrap must not clear a newer selection');
  assert.equal(await loading.isVisible(), true);
  assert.equal(count('/api/orchestrations/a/progress'), 0);
  assert.equal(count('/api/orchestrations/a/events'), 1, 'first history starts before detail resolves');
  release('/api/orchestrations/a');
  assert.equal(count('/api/orchestrations/a/progress'), 0, 'progress must wait for first-page history');
  release('/api/orchestrations/a/events');
  await until(() => count('/api/orchestrations/a/progress') === 1);
  await loading.waitFor({ state: 'hidden' });
  await page.waitForTimeout(250);
  assert.equal(count('/api/orchestrations/a/progress'), 1, 'initial progress must not duplicate');

  hold('/api/orchestrations/b');
  await select('b'); await until(() => count('/api/orchestrations/b') === 1);
  await select('a'); await until(() => aborted('/api/orchestrations/b'));
  release('/api/orchestrations/b'); await loading.waitFor({ state: 'hidden' });
  assert.equal(new URL(page.url()).pathname, '/orchestrate/runs/a');
  assert.equal(await page.getByText('History b', { exact: true }).count(), 0, 'late detail cannot activate previous selection');

  failures.set('/api/orchestrations/fail', 503);
  await select('fail'); await page.getByRole('alert').getByText('Fixture unavailable').waitFor();
  assert.equal(await page.locator('aside').last().getByRole('button').count(), 0, 'failed selection must not expose send controls');
  failures.delete('/api/orchestrations/fail');
  await page.getByRole('alert').getByRole('button', { name: '重试', exact: true }).click();
  await until(() => count('/api/orchestrations/fail/progress') === 1);

  await select('active'); await until(() => count('/api/orchestrations/active/progress') === 1);
  hold('/api/orchestrations/active/events');
  const initialCount = count('/api/orchestrations/active/events');
  await until(() => count('/api/orchestrations/active/events') === initialCount + 1);
  await page.waitForTimeout(6400);
  assert.equal(count('/api/orchestrations/active/events'), initialCount + 1, 'slow polling must not accumulate');
  await select('a'); await until(() => aborted('/api/orchestrations/active/events?afterSeq=1&limit=1000'));
  release('/api/orchestrations/active/events');
  console.log('Conversation priority, single initial progress, switch cancellation, retry and non-overlapping polling passed.');

  const admin30 = '/api/admin/usage?days=30&timezoneOffset=0';
  hold(admin30); await navigate('/admin/usage'); await until(() => count(admin30) === 1);
  await page.getByRole('button', { name: '近 7 天', exact: true }).click();
  await page.getByText('User range-7', { exact: true }).first().waitFor();
  await until(() => aborted(admin30)); release(admin30);
  await page.waitForTimeout(100);
  assert.equal(await page.getByText('User range-30', { exact: true }).count(), 0);
  const userA = '/api/admin/users/a/usage?days=30&timezoneOffset=0';
  hold(userA); await navigate('/admin/usage/users/a'); await until(() => count(userA) === 1);
  await navigate('/admin/usage/users/b'); await page.getByText('User b', { exact: true }).first().waitFor();
  await until(() => aborted(userA)); release(userA);
  const contentOne = '/api/admin/users/b/conversations/chat/one?limit=200';
  hold(contentOne); await page.getByText('Conversation one', { exact: true }).first().click();
  await until(() => count(contentOne) === 1);
  await page.keyboard.press('Escape'); await until(() => aborted(contentOne));
  await page.getByText('Conversation two', { exact: true }).first().click();
  await page.getByText('Content two', { exact: true }).waitFor(); release(contentOne);
  await page.waitForTimeout(100);
  assert.equal(await page.getByText('Content one', { exact: true }).count(), 0);
  await page.keyboard.press('Escape');
  failures.set('/api/admin/usage?days=30&timezoneOffset=0', 504);
  await navigate('/admin/usage'); await page.getByRole('alert').getByText('统计请求超时，请重试。').waitFor();
  failures.clear(); await page.getByRole('alert').getByRole('button', { name: '重试', exact: true }).click();
  await page.getByText('User range-30', { exact: true }).first().waitFor();
  console.log('Admin range/user/content cancellation, stale-response isolation, timeout and retry passed.');
  assert.deepEqual(errors, []);
} finally { await browser.close(); }
