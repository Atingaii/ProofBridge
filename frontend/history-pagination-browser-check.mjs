import assert from 'node:assert/strict';
import { chromium } from 'playwright';
const baseURL = process.env.SITE_LOADING_TEST_URL || 'http://127.0.0.1:37638';
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const gate = () => { let release; const promise = new Promise(resolve => { release = resolve; }); return { promise, release }; };
const until = async (check, message) => { const deadline = Date.now() + 30000; while (!await check()) { assert.ok(Date.now() < deadline, message); await pause(30); } };
const browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 960 } });
  page.setDefaultTimeout(30000);
  const errors = [], requests = [], detail = gate(), firstCatchup = gate(), cancelledCatchup = gate();
  const sockets = new Map();
  let cancelCatchup = false, catchupHeld = false, failAdminPage = true;
  page.on('pageerror', error => errors.push(error.message));
  const run = id => ({ id, agentId: 'agent', title: `Fixture ${id}`, prompt: `Initial ${id}`, mode: 'collaboration', workerPair: 'codex-codex', maxTurns: 0, status: id === 'a' ? 'running' : 'completed', createdAt: 1, updatedAt: 1 });
  const event = (seq, content, id = 'a') => ({ id: `${id}-${seq}`, runId: id, seq, kind: 'user.message', content, createdAt: seq });
  const metric = { totalTokens: 10, inputTokens: 8, outputTokens: 2, cacheTokens: 0, callCount: 1, costKnown: true, estimatedCostUsd: .1 };
  await page.addInitScript(() => { localStorage.setItem('codexBridge.language', 'zh'); window.__aborts = []; const realFetch = window.fetch.bind(window); window.fetch = (url, init) => { init?.signal?.addEventListener('abort', () => window.__aborts.push(String(url))); return realFetch(url, { ...init, signal: undefined }); }; });
  await page.route('**/api/**', async route => {
    assert.equal(route.request().method(), 'GET', 'fixtures never submit model tasks');
    const url = new URL(route.request().url()), path = url.pathname, q = url.searchParams;
    requests.push(path + url.search);
    let json = {};
    if (path === '/api/me') json = { user: { id: 'fixture', username: 'fixture', isAdmin: true, features: ['orchestration-plan-workspace'] } };
    else if (path === '/api/agents') json = { agents: [{ id: 'agent', name: 'Machine', online: true, lastSeenAt: 1, capabilities: { orchestration: { codex: { available: true }, claude: { available: true } } } }] };
    else if (path === '/api/orchestrations') json = { runs: [run('a'), run('b')] };
    else if (/\/orchestrations\/[^/]+$/.test(path)) { if (path.endsWith('/a')) await detail.promise; json = { run: run(path.split('/')[3]) }; }
    else if (path.endsWith('/events')) {
      const id = path.split('/')[3];
      if (q.has('afterSeq')) {
        const after = Number(q.get('afterSeq'));
        if (id === 'a' && after === 100) { await firstCatchup.promise; json = { events: [event(101, 'Catchup first page')], hasMoreAfter: true }; }
        else if (id === 'a' && after === 101) json = { events: [event(102, 'Catchup second page')], hasMoreAfter: false };
        else if (id === 'a' && cancelCatchup) { catchupHeld = true; await cancelledCatchup.promise; json = { events: [event(103, 'Cancelled history')], hasMoreAfter: true }; }
        else json = { events: [], hasMoreAfter: false };
      } else if (q.has('beforeSeq')) json = { events: Array.from({ length: 20 }, (_, i) => event(i + 1, `Older message ${i + 1}\n${'Historical detail '.repeat(25)}`, id)), hasMoreBefore: false };
      else json = { events: [id === 'b' ? { runId: id, seq: 100, kind: 'turn.usage', data: { totalTokens: 1 } } : event(100, 'Latest visible message', id)], hasMoreBefore: true };
    } else if (path.endsWith('/progress')) json = { graph: null, graphs: [], tasks: [], planItems: [], plan: null };
    else if (/\/admin\/users\/[^/]+\/usage$/.test(path)) json = { detail: { ...metric, username: 'Fixture user', conversations: ['chat', 'orchestration'].map(kind => ({ ...metric, id: kind, kind, title: `Content ${kind}`, agentName: 'Machine', updatedAt: 1, activityCount: 1, status: 'completed' })) } };
    else if (path.includes('/conversations/')) {
      const kind = path.split('/').at(-1), base = { id: kind, kind, title: kind };
      const item = content => ({ kind: 'assistant', role: 'assistant', createdAt: 0, content });
      if (kind === 'chat') {
        if (!q.has('beforeId')) json = { conversation: { ...base, items: [item('Newest chat page')], hasMoreBefore: true, nextBeforeCreatedAt: 0, nextBeforeId: 'b' } };
        else { assert.equal(q.get('beforeCreatedAt'), '0'); assert.equal(q.get('beforeId'), 'b'); json = { conversation: { ...base, items: [item('Oldest same-second chat page')], hasMoreBefore: false } }; }
      } else if (!q.has('beforeSeq')) json = { conversation: { ...base, items: [item('Newest orchestration fragment')], hasMoreBefore: true, nextBeforeSeq: 200 } };
      else if (q.get('beforeSeq') === '200') json = { conversation: { ...base, items: [], hasMoreBefore: true, nextBeforeSeq: 100 } };
      else { assert.equal(q.get('beforeSeq'), '100'); if (failAdminPage) return route.fulfill({ status: 504, json: {} }); json = { conversation: { ...base, items: [item('Oldest orchestration fragment')], hasMoreBefore: false } }; }
    }
    await route.fulfill({ json }).catch(() => {});
  });
  await page.routeWebSocket('**/ws/**', ws => { const id = new URL(ws.url()).pathname.split('/').at(-1); sockets.set(id, ws); ws.onMessage(() => {}); });
  const count = path => requests.filter(request => request === path).length;
  const navigate = path => page.evaluate(next => { history.pushState({}, '', next); window.dispatchEvent(new PopStateEvent('popstate')); }, path);
  await page.goto(`${baseURL}/orchestrate/runs/a`, { waitUntil: 'domcontentloaded' });
  await until(() => count('/api/orchestrations/a/events?limit=100') === 1, 'history did not start in parallel');
  assert.equal(count('/api/orchestrations/a'), 1, 'deep-link detail must only start once');
  assert.equal(count('/api/orchestrations/a/progress'), 0);
  detail.release();
  await page.getByText('Latest visible message', { exact: true }).waitFor();
  await until(() => count('/api/orchestrations/a/events?afterSeq=100&limit=1000') === 1, 'initial catchup missing');
  await until(() => sockets.size > 0, 'socket missing');
  [...sockets.values()][0].send(JSON.stringify({ type: 'orchestration_event', payload: event(1000, 'Live event ahead of catchup') }));
  await page.getByText('Live event ahead of catchup', { exact: true }).waitFor();
  firstCatchup.release();
  await page.getByText('Catchup second page', { exact: true }).waitFor();
  assert.equal(count('/api/orchestrations/a/events?afterSeq=101&limit=1000'), 1, 'WS maximum must not skip second HTTP page');
  const scroller = page.locator('.elegant-scrollbar').filter({ has: page.getByText('Latest visible message', { exact: true }) }).last();
  await scroller.evaluate(node => { node.scrollTop = 0; node.dispatchEvent(new Event('scroll', { bubbles: true })); });
  await pause(300);
  assert.equal(requests.filter(path => path.includes('beforeSeq=')).length, 0, 'scrolling alone must not fetch history');
  await page.getByRole('button', { name: '加载更早记录', exact: true }).click();
  await until(() => count('/api/orchestrations/a/events?beforeSeq=100&limit=300') === 1, 'manual history missing');
  await page.getByText('Older message 1', { exact: false }).first().waitFor();
  await page.getByText('Latest visible message', { exact: true }).waitFor();
  await until(() => scroller.evaluate(node => node.scrollTop > 0), 'prepend did not preserve scroll anchor');
  cancelCatchup = true;
  await until(() => catchupHeld, 'continuous HTTP watermark missing');
  await navigate('/orchestrate/runs/b');
  await until(() => page.evaluate(() => window.__aborts.includes('/api/orchestrations/a/events?afterSeq=102&limit=1000')), 'catchup did not abort');
  cancelledCatchup.release();
  await page.getByRole('status', { name: '正在加载对话…', exact: true }).waitFor({ state: 'hidden' });
  assert.equal(count('/api/orchestrations/a/events?afterSeq=103&limit=1000'), 0, 'canceled chain fetched another page');
  await page.getByRole('button', { name: '加载更早记录', exact: true }).click();
  await page.getByText('Older message 1', { exact: false }).first().waitFor();
  assert.equal(count('/api/orchestrations/b/events?beforeSeq=100&limit=300'), 1, 'hidden-only latest page must keep earlier history accessible');
  await navigate('/admin/usage/users/fixture');
  await page.getByText('Content chat', { exact: true }).first().click();
  await page.getByText('Newest chat page', { exact: true }).waitFor();
  await page.getByRole('button', { name: '加载更早记录', exact: true }).click();
  await page.getByText('Oldest same-second chat page', { exact: true }).waitFor();
  await page.getByText('Newest chat page', { exact: true }).waitFor();
  await page.keyboard.press('Escape');
  await page.getByText('Content orchestration', { exact: true }).first().click();
  await page.getByText('Newest orchestration fragment', { exact: true }).waitFor();
  await page.getByRole('button', { name: '加载更早记录', exact: true }).click();
  await until(() => count('/api/admin/users/fixture/conversations/orchestration/orchestration?limit=200&beforeSeq=200') === 1, 'empty source page missing');
  await page.getByRole('button', { name: '加载更早记录', exact: true }).click();
  await page.getByRole('alert').getByText('对话内容加载超时，请重试。').waitFor();
  await page.getByText('Newest orchestration fragment', { exact: true }).waitFor();
  failAdminPage = false;
  await page.getByRole('alert').getByRole('button', { name: '重试', exact: true }).click();
  await page.getByText('Oldest orchestration fragment', { exact: true }).waitFor();
  await page.getByText('Newest orchestration fragment', { exact: true }).waitFor();
  assert.deepEqual(errors, []);
  console.log('History pagination: parallel deep link, explicit older pages, scroll anchor, WS-racing serial catchup, cancellation, timestamp-zero chat cursor, empty admin source page and retry passed.');
} finally { await browser.close(); }
