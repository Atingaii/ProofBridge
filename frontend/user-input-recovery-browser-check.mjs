import assert from 'node:assert/strict';
import { chromium } from 'playwright';

const baseURL = process.env.USER_INPUT_TEST_URL || 'http://127.0.0.1:4199';
const run = { id: 'recovery-fixture', agentId: 'agent-fixture', title: 'Recovery fixture', mode: 'collaboration', workerPair: 'codex-codex',
  prompt: 'Fixture', maxTurns: 2, status: 'running', createdAt: 1, updatedAt: 1 };
const pending = { userId: 'user-fixture', runId: run.id, requestId: 'question', submissionId: 'answer',
  executionId: 'exec', instanceId: 'bridge', createdAt: 1 };
const request = { requestId: 'question', runId: run.id, executionId: 'exec', instanceId: 'bridge', cli: 'codex',
  blocking: true, revision: 1, questions: [{ id: 'q', question: 'Choose a path', selectionMode: 'single',
    options: [{ id: 'a', label: 'A' }] }] };
const wait = async (predicate) => {
  const until = Date.now() + 60000;
  while (!predicate()) { assert.ok(Date.now() < until, 'transport did not converge'); await new Promise(r => setTimeout(r, 20)); }
};
const browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
const pages = [];
async function fixture({ stored = [], storageError = false, terminal = false } = {}) {
  if (pages.length) await pages.at(-1).close();
  console.log('Recovery fixture', pages.length + 1, { stored: stored.length, storageError, terminal });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  pages.push(page);
  page.setDefaultTimeout(30000);
  await page.clock.install();
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.addInitScript(({ stored, storageError }) => {
    localStorage.setItem('codexBridge.language', 'zh');
    sessionStorage.setItem('codexBridge.nativeInputPending.v3:user-fixture', JSON.stringify(stored));
    if (storageError) {
      const get = Storage.prototype.getItem;
      Storage.prototype.getItem = function(key) {
        if (this === sessionStorage && key.startsWith('codexBridge.nativeInputPending')) throw new Error('unavailable');
        return get.call(this, key);
      };
    }
  }, { stored, storageError });
  await page.route('**/api/**', async route => {
    const path = new URL(route.request().url()).pathname;
    let data = {};
    if (path === '/api/me') data = { nativeUserInputProtocolVersion: 3, user: { id: 'user-fixture', username: 'fixture', createdAt: 1, features: ['native-user-input'] } };
    else if (path === '/api/agents') data = { agents: [{ id: run.agentId, name: 'Fixture', machineId: 'fixture', hostname: 'fixture', online: true, lastSeenAt: 1,
      capabilities: { orchestration: { codex: { available: true } }, nativeUserInput: { codex: { available: true, version: 3 } } } }] };
    else if (path === '/api/orchestrations') data = { runs: [{ ...run, status: terminal ? 'completed' : 'running' }] };
    else if (path === `/api/orchestrations/${run.id}`) data = { run: { ...run, status: terminal ? 'completed' : 'running' } };
    else if (path.endsWith('/events')) data = { events: [], hasMoreBefore: false };
    else if (path.endsWith('/prompts')) data = { messages: [] };
    else if (path.includes('/presets')) data = { presets: [] };
    await route.fulfill({ json: data });
  });
  let socket;
  const frames = [];
  await page.routeWebSocket(`**/ws/orchestrations?runId=${run.id}`, ws => {
    socket = ws;
    ws.onMessage(data => frames.push(JSON.parse(data)));
  });
  await page.goto(`${baseURL}/orchestrate/runs/${run.id}`, { waitUntil: 'domcontentloaded', timeout: 90000 });
  const syncs = () => frames.filter(f => f.type === 'user_input_sync');
  try { await wait(() => syncs().length > 0); } catch (error) {
    console.error({ errors, frames, pageText: (await page.locator('body').innerText()).slice(0, 1500) });
    throw error;
  }
  const send = (type, payload) => socket.send(JSON.stringify({ type, payload }));
  const snapshot = (patch = {}) => send('user_input_snapshot', { runId: run.id, syncId: syncs().at(-1).payload.syncId,
    instanceId: 'bridge', revision: 1, requests: [], ...patch });
  const rejection = (patch = {}) => send('user_input_status', { requestId: '', runId: run.id,
    syncId: syncs().at(-1).payload.syncId, status: 'rejected', source: 'hub', phase: 'route', revision: 0,
    reason: 'CLI endpoint is offline', ...patch });
  const noReplay = () => { assert.equal(frames.filter(f => f.type === 'user_input_response').length, 0); assert.deepEqual(errors, []); };
  const exhaust = async () => {
    for (const delay of [10000, 1000, 10000, 2000, 10000]) await page.clock.fastForward(delay);
  };
  return { page, frames, syncs, send, snapshot, rejection, noReplay, exhaust };
}
try {
  // Initial problem-list discovery has no submitted answer to lose.
  {
    const f = await fixture();
    await f.exhaust();
    await f.page.getByText('暂时无法同步结构化问题；请重新查询。', { exact: true }).waitFor();
    assert.equal(await f.page.getByText(/暂时无法确认答案是否送达/).count(), 0);
    const oldSync = f.syncs().at(-1).payload.syncId;
    f.snapshot();
    await f.page.getByText('暂时无法同步结构化问题；请重新查询。', { exact: true }).waitFor({ state: 'hidden' });
    f.rejection({ syncId: oldSync }); // An error arriving after success must stay obsolete.
    await f.page.clock.runFor(100);
    assert.equal(await f.page.getByText(/CLI 端点离线/).count(), 0);
    f.noReplay();
  }
  for (const reason of ['CLI endpoint is offline', 'Bridge needs protocol v3 for structured input']) {
    const f = await fixture();
    f.rejection({ runId: 'other-run', reason });
    f.rejection({ syncId: 'old-query', reason });
    await f.page.clock.runFor(100);
    assert.equal(await f.page.getByText(/暂时无法同步结构化问题/).count(), 0);
    f.rejection({ reason });
    await f.page.getByText(reason.includes('offline') ? /CLI 端点离线/ : /当前 Bridge 不支持所需问答协议/).waitFor();
    assert.equal(f.syncs().length, 1, 'explicit rejection should stop the timer immediately');
    const oldSync = f.syncs().at(-1).payload.syncId;
    await f.page.getByRole('button', { name: '重新查询', exact: true }).click();
    await wait(() => f.syncs().length === 2);
    f.rejection({ syncId: oldSync });
    f.snapshot();
    await f.page.getByRole('button', { name: '重新查询', exact: true }).waitFor({ state: 'hidden' });
    f.noReplay();
  }
  // Existing pending metadata must retain delivery wording even before instance discovery.
  {
    const f = await fixture({ stored: [pending] });
    await f.exhaust();
    await f.page.getByText(/暂时无法确认答案是否送达/).waitFor();
    f.send('user_input_status', { ...pending, status: 'delivered', source: 'bridge', revision: 2,
      receipt: { ...pending, outcome: 'delivered', revision: 2 } });
    await f.page.getByRole('button', { name: '重新查询', exact: true }).waitFor({ state: 'hidden' });
    f.noReplay();
  }
  // Receipt recovery does not discard initial discovery of a different unanswered question.
  {
    const f = await fixture({ stored: [pending] });
    f.send('user_input_status', { ...pending, status: 'delivered', source: 'bridge', revision: 2,
      receipt: { ...pending, outcome: 'delivered', revision: 2 } });
    f.snapshot({ revision: 3, requests: [{ ...request, requestId: 'new-question', revision: 3 }] });
    await f.page.locator('#native-user-input-new-question').waitFor({ state: 'visible' });
    f.noReplay();
  }
  // A stale compatibility snapshot without instanceId is not a replacement Bridge.
  {
    const f = await fixture();
    f.snapshot({ revision: 10 });
    await f.page.clock.runFor(100);
    // Submit once to query within the established revision domain.
    f.send('user_input_request', { ...request, revision: 11 });
    const card = f.page.locator('#native-user-input-question');
    await card.getByRole('radio', { name: 'A', exact: true }).check();
    await card.getByRole('button', { name: '提交回答', exact: true }).click();
    await wait(() => f.syncs().length === 2);
    f.rejection();
    await f.page.getByText(/CLI 端点离线/).waitFor();
    f.snapshot({ instanceId: undefined, revision: 1 });
    await f.page.clock.runFor(100);
    assert.equal(await f.page.getByText(/CLI 端点离线/).count(), 1);
    assert.equal(f.frames.filter(frame => frame.type === 'user_input_response').length, 1);
  }
  // A real explicit submit is sent once; retries only query until a late receipt arrives.
  {
    const f = await fixture();
    f.snapshot({ requests: [request] });
    const card = f.page.locator('#native-user-input-question');
    await card.getByRole('radio', { name: 'A', exact: true }).check();
    await card.getByRole('button', { name: '提交回答', exact: true }).click();
    await wait(() => f.frames.some(frame => frame.type === 'user_input_response'));
    const answer = f.frames.find(frame => frame.type === 'user_input_response').payload;
    f.rejection();
    await f.page.getByText(/暂时无法确认答案是否送达/).waitFor();
    const before = f.syncs().length;
    await f.page.getByRole('button', { name: '重新查询', exact: true }).click();
    await wait(() => f.syncs().length > before);
    f.send('user_input_status', { ...pending, submissionId: answer.submissionId, status: 'delivered', source: 'bridge', revision: 2,
      receipt: { ...pending, submissionId: answer.submissionId, outcome: 'delivered', revision: 2 } });
    await f.page.getByRole('button', { name: '重新查询', exact: true }).waitFor({ state: 'hidden' });
    assert.equal(f.frames.filter(frame => frame.type === 'user_input_response').length, 1, 'queries must never resend the answer');
  }
  // Explicitly unknown delivery is never cleared by an empty snapshot or a Hub error.
  {
    const f = await fixture({ stored: [pending] });
    f.snapshot({ requests: [request] });
    await wait(() => f.syncs().length === 2);
    f.send('user_input_status', { ...pending, status: 'rejected', source: 'bridge', revision: 2,
      receipt: { ...pending, outcome: 'unknown', revision: 2 } });
    f.rejection();
    await f.page.getByText(/暂时无法确认答案是否送达/).waitFor();
    f.snapshot({ revision: 3 });
    await f.page.clock.runFor(100);
    assert.equal(await f.page.evaluate(() => JSON.parse(sessionStorage.getItem('codexBridge.nativeInputPending.v3:user-fixture')).length), 1);
    assert.equal(await f.page.getByRole('button', { name: '重新查询', exact: true }).count(), 1);
    f.noReplay();
  }
  {
    const f = await fixture({ stored: [pending] });
    f.snapshot({ instanceId: 'replacement-bridge', revision: 1 });
    await f.page.getByText('有答案等待确认送达；重新查询不会重发答案。', { exact: true }).waitFor();
    assert.equal(await f.page.getByText('答案已送达 CLI', { exact: true }).count(), 0);
    f.noReplay();
  }
  {
    const f = await fixture({ storageError: true });
    await f.page.getByText(/无法读取未确认提交记录/).waitFor();
    f.snapshot();
    await f.page.clock.runFor(100);
    assert.equal(await f.page.getByText(/无法读取未确认提交记录/).count(), 1, 'successful sync cannot clear storage failures');
    f.noReplay();
  }
  {
    const f = await fixture({ stored: [pending], terminal: true });
    f.snapshot({ revision: 3, receipts: [{ ...pending, outcome: 'delivered', revision: 2 }] });
    await f.page.getByRole('button', { name: '重新查询', exact: true }).waitFor({ state: 'hidden' });
    f.noReplay();
    assert.ok(f.frames.every(frame => ['user_input_sync', 'heartbeat'].includes(frame.type)), 'terminal socket is read-only');
  }
  console.log('Desktop structured-input recovery: empty timeout, correlated errors, late recovery, unknown, storage, and terminal read-only passed.');
} finally {
  await Promise.all(pages.map(page => page.close()));
  await browser.close();
}
