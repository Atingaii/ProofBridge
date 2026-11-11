import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { chromium } from 'playwright';

// All HTTP and WebSocket traffic is intercepted; this suite never runs a model.
const baseURL = process.env.DESKTOP_FIXES_TEST_URL || 'http://127.0.0.1:37628';
const screenshotDir = process.env.DESKTOP_FIXES_SCREENSHOT_DIR;
if (screenshotDir) await mkdir(screenshotDir, { recursive: true });
const waitUntil = async (predicate, message = 'fixture state did not converge') => {
  const deadline = Date.now() + 20000;
  while (!await predicate()) {
    assert.ok(Date.now() < deadline, message);
    await new Promise((resolve) => setTimeout(resolve, 30));
  }
};
const gate = () => { let release; const promise = new Promise((resolve) => { release = resolve; }); return { promise, release }; };
const browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
let page;
const errors = [];
try {
  page = await browser.newPage({ viewport: { width: 1440, height: 960 } });
  page.setDefaultTimeout(30000);
  page.on('pageerror', (error) => errors.push(error.message));
  const makeRun = (id, createdAt, extra = {}) => ({ id, agentId: 'agent-a', title: `Fixture ${id}`, prompt: `Initial task ${id}`,
    mode: 'collaboration', workerPair: 'codex-codex', executionMode: 'dynamic', maxTurns: 0,
    status: 'completed', createdAt, updatedAt: createdAt, ...extra });
  const runs = [makeRun('run-a', 1000), makeRun('run-b', 999), makeRun('run-active', 998, { status: 'running' }),
    ...Array.from({ length: 225 }, (_, i) => makeRun(`older-${i}`, 700 - i)),
    makeRun('literal-old', 1, { title: 'Literal %_ old task', status: 'failed' }),
    makeRun('archived-old', 0, { archivedAt: 100 }), makeRun('other-endpoint-old', 0, { agentId: 'agent-b' })];
  const agents = ['a', 'b'].map((id) => ({ id: `agent-${id}`, name: `Fixture machine ${id}`, machineId: id, hostname: id,
    online: true, lastSeenAt: 1, workingDirs: ['/tmp/fixture'], capabilities: { approvalPolicy: 'never', sandbox: 'danger-full-access',
      orchestrationLiveInput: 1, orchestration: { codex: { available: true }, claude: { available: true } } } }));
  let account = 'user-a', auth = 'ok', authHeld, postHeld, patchHeld, slowHistory;
  let failPost = false, failHistory = false, agentReads = 0, createdRuns = 0;
  const posts = [], patches = [], queries = [];
  const messages = new Map();
  await page.addInitScript(() => localStorage.setItem('codexBridge.language', 'zh'));
  await page.route('**/api/**', async (route) => {
    const url = new URL(route.request().url());
    const method = route.request().method();
    let data = {};
    if (url.pathname === '/api/me') {
      if (auth === 'network') return route.abort('failed');
      if (auth === '503' || auth === '401') return route.fulfill({ status: Number(auth), json: { message: 'Fixture auth error' } });
      if (auth === 'hang') { authHeld = gate(); await authHeld.promise; }
      data = { user: { id: account, username: account, createdAt: 1, features: [] } };
    } else if (url.pathname === '/api/agents') { agentReads += 1; data = { agents }; }
    else if (url.pathname.includes('/presets')) data = { presets: [{ id: 'preset-codex', cli: 'codex', name: 'Fixture Codex', model: 'fixture' }, { id: 'preset-claude', cli: 'claude', name: 'Fixture Claude', model: 'fixture' }] };
    else if (url.pathname === '/api/orchestrations' && method === 'GET') {
      queries.push(new URLSearchParams(url.search));
      assert.equal(url.searchParams.get('limit'), '50');
      assert.ok(url.searchParams.get('agentId'), 'history must filter on the server before the limit');
      if (failHistory) return route.fulfill({ status: 503, json: { message: 'Fixture history unavailable' } });
      const q = url.searchParams.get('q') || '';
      const archived = url.searchParams.get('archived');
      const filtered = runs.filter((run) => run.agentId === url.searchParams.get('agentId')
        && (!q || `${run.title} ${run.prompt}`.toLowerCase().includes(q.toLowerCase()))
        && (!url.searchParams.get('status') || run.status === url.searchParams.get('status'))
        && (archived === 'all' || Boolean(run.archivedAt) === (archived === 'true')))
        .sort((a, b) => b.createdAt - a.createdAt || b.id.localeCompare(a.id));
      const offset = Number((url.searchParams.get('cursor') || 'page:0').split(':')[1]);
      data = { runs: structuredClone(filtered.slice(offset, offset + 50)), ...(offset + 50 < filtered.length ? { nextCursor: `page:${offset + 50}` } : {}) };
      if (q === 'slow') { slowHistory = gate(); await slowHistory.promise; }
    } else if (url.pathname === '/api/orchestrations' && method === 'POST' || url.pathname.endsWith('/prompts') && method === 'POST') {
      const body = route.request().postDataJSON();
      const targetId = url.pathname.split('/')[3];
      posts.push({ targetId, body });
      const held = postHeld; if (held) await held.promise;
      if (failPost) return route.fulfill({ status: 409, json: { message: 'Fixture send rejected' } });
      if (body.messageId) {
        const list = messages.get(targetId) || [];
        list.push({ ...body, id: body.messageId, runId: targetId, status: 'queued', createdAt: 1, updatedAt: 1 });
        messages.set(targetId, list); data = { message: list.at(-1) };
      } else {
        let run = runs.find((item) => item.id === targetId);
        if (!run) { run = makeRun(`created-${++createdRuns}`, 2000 + createdRuns, { agentId: body.agentId }); runs.push(run); }
        delete run.archivedAt;
        data = { run };
      }
    } else if (url.pathname.endsWith('/prompts')) data = { messages: messages.get(url.pathname.split('/')[3]) || [] };
    else if (url.pathname.endsWith('/events')) data = { events: [], hasMoreBefore: false };
    else if (/^\/api\/orchestrations\/[^/]+$/.test(url.pathname)) {
      const run = runs.find((item) => item.id === url.pathname.split('/')[3]);
      if (method === 'PATCH') {
        const body = route.request().postDataJSON(); patches.push({ id: run.id, body });
        const held = patchHeld; if (held) await held.promise;
        if (body.title !== undefined) run.title = body.title;
        if (body.archived !== undefined) { if (body.archived) run.archivedAt = 1234; else delete run.archivedAt; }
      }
      data = { run };
    } else {
      assert.ok(method === 'GET' || url.pathname === '/api/logout', `unexpected mutation: ${method} ${url.pathname}`);
    }
    await route.fulfill({ json: data }).catch(() => {}); // Requests may be canceled deliberately.
  });
  await page.routeWebSocket('**/ws/**', (ws) => ws.onMessage(() => {}));
  const input = page.locator('textarea:visible').first();
  const history = page.locator('[aria-label="运行历史"]');
  const row = (id) => history.locator(`[data-run-id="${id}"]`);
  const select = async (id) => { await row(id).getByRole('button', { name: runs.find((run) => run.id === id).title, exact: true }).click(); await input.waitFor(); };
  const assertInput = async (expected) => waitUntil(async () => await input.inputValue() === expected, `draft should be ${expected}`);
  const screenshot = async (name) => { if (screenshotDir) await page.screenshot({ path: `${screenshotDir}/${name}.png`, fullPage: true }); };
  const continueButton = () => page.getByRole('button', { name: /^(继续运行|继续)$/ }).last();
  const submitHeld = async (text) => {
    await input.fill(text); postHeld = gate(); const count = posts.length;
    await continueButton().click(); await waitUntil(() => posts.length === count + 1);
  };
  const finishPost = async () => { const held = postHeld; postHeld = undefined; held.release(); await waitUntil(async () => !await continueButton().isDisabled()); };

  auth = '503';
  await page.goto(`${baseURL}/orchestrate/runs/run-a`, { waitUntil: 'domcontentloaded' });
  await page.getByText('暂时无法验证登录状态', { exact: true }).waitFor();
  assert.equal(await page.getByRole('button', { name: '登录', exact: true }).count(), 0);
  await screenshot('auth-service-retry');
  auth = 'ok'; await page.getByRole('button', { name: '重试', exact: true }).click(); await input.waitFor();
  assert.equal(new URL(page.url()).pathname, '/orchestrate/runs/run-a');
  auth = 'network'; await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByText('暂时无法验证登录状态', { exact: true }).waitFor();
  auth = 'ok'; await page.getByRole('button', { name: '重试', exact: true }).click(); await input.waitFor();
  auth = 'hang'; await page.reload({ waitUntil: 'domcontentloaded' });
  await waitUntil(() => Boolean(authHeld));
  await page.getByText('验证登录状态超时。请重试，恢复后将继续打开当前页面。', { exact: true }).waitFor();
  auth = 'ok'; authHeld.release();
  await page.getByRole('button', { name: '重试', exact: true }).click(); await input.waitFor();
  auth = '401'; await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByLabel('用户名', { exact: true }).waitFor();
  auth = 'ok'; await page.reload({ waitUntil: 'domcontentloaded' }); await input.waitFor();
  console.log('auth bootstrap recovery passed');

  await input.fill('A draft'); await select('run-b'); await assertInput('');
  await input.fill('B draft'); await select('run-a'); await assertInput('A draft');
  await page.reload({ waitUntil: 'domcontentloaded' }); await input.waitFor(); await assertInput('A draft');
  await page.getByRole('button', { name: '新运行', exact: true }).click(); await input.fill('new machine A');
  const endpoint = page.locator('select[aria-label="选择 CLI 端"]:visible').first();
  await endpoint.selectOption('agent-b'); await assertInput(''); await input.fill('new machine B');
  await row('other-endpoint-old').waitFor();
  assert.ok(queries.some((query) => query.get('agentId') === 'agent-b'));
  await endpoint.selectOption('agent-a'); await assertInput('new machine A');
  await page.getByRole('button', { name: '新运行', exact: true }).click(); await assertInput('new machine A');
  await page.reload({ waitUntil: 'domcontentloaded' }); await input.waitFor(); await assertInput('new machine A');
  await select('run-a'); await assertInput('A draft');
  account = 'user-b'; await page.reload({ waitUntil: 'domcontentloaded' }); await input.waitFor(); await assertInput('');
  await input.fill('separate account draft'); account = 'user-a'; await page.reload({ waitUntil: 'domcontentloaded' }); await input.waitFor(); await assertInput('A draft');

  await submitHeld('A submitted text'); await select('run-b'); await input.fill('B edited while A sends');
  await finishPost(); await assertInput('B edited while A sends');
  assert.equal(new URL(page.url()).pathname, '/orchestrate/runs/run-b');
  await select('run-a'); await assertInput('');
  await submitHeld('A first revision'); await input.fill('A newer revision'); await finishPost(); await assertInput('A newer revision');
  failPost = true; await continueButton().click(); await page.getByText('Fixture send rejected', { exact: true }).waitFor();
  await assertInput('A newer revision'); failPost = false;
  assert.ok(posts.every((post) => post.targetId === 'run-a'), 'follow-ups must retain the run id');
  await screenshot('isolated-run-draft');

  // This path exists in the leading build, where live input has its own POST.
  if (await page.getByRole('region', { name: '运行对话', exact: true }).count()) {
    await select('run-active'); await input.fill('live submitted'); postHeld = gate(); let count = posts.length;
    await page.getByRole('button', { name: '排队发送', exact: true }).click(); await waitUntil(() => posts.length === count + 1);
    await input.fill('live newer'); const held = postHeld; postHeld = undefined; held.release();
    await page.getByText('live submitted', { exact: true }).waitFor(); await assertInput('live newer');
    await input.fill('live switching'); postHeld = gate(); count = posts.length;
    await page.getByRole('button', { name: '排队发送', exact: true }).click(); await waitUntil(() => posts.length === count + 1);
    await select('run-b'); const secondHeld = postHeld; postHeld = undefined; secondHeld.release();
    await assertInput('B edited while A sends'); await waitUntil(() => messages.get('run-active')?.length === 2);
    await select('run-active'); await assertInput(''); await select('run-a');
  }

  await page.getByRole('button', { name: '新运行', exact: true }).click(); await assertInput('new machine A');
  for (const profile of await page.locator('select[aria-label$="工作器模型预设"]').all()) await profile.selectOption('preset-codex');
  await input.fill('new run submitted'); postHeld = gate(); let createPostCount = posts.length;
  await page.getByRole('button', { name: '开始', exact: true }).click(); await waitUntil(() => posts.length > createPostCount);
  await input.fill('new run typed later'); const newRunHeld = postHeld; postHeld = undefined; newRunHeld.release();
  await waitUntil(() => createdRuns === 1); await assertInput('new run typed later');
  assert.equal(new URL(page.url()).pathname, '/orchestrate', 'newer new-run text stays in its composer');
  await waitUntil(async () => !await page.getByRole('button', { name: '开始', exact: true }).isDisabled());
  postHeld = gate(); createPostCount = posts.length;
  await page.getByRole('button', { name: '开始', exact: true }).click(); await waitUntil(() => posts.length > createPostCount);
  await endpoint.selectOption('agent-b'); await assertInput('new machine B');
  const switchedNewHeld = postHeld; postHeld = undefined; switchedNewHeld.release();
  await waitUntil(() => createdRuns === 2);
  await waitUntil(async () => await page.evaluate(() => localStorage.getItem('codexBridge.orchestrationDraft.v1:user-a:new:agent-a')) === null);
  await assertInput('new machine B'); assert.equal(new URL(page.url()).pathname, '/orchestrate');
  await endpoint.selectOption('agent-a'); await assertInput(''); await select('run-a'); await assertInput('A newer revision');

  while (await history.getByRole('button', { name: '加载更多运行', exact: true }).count()) {
    const before = await history.locator('[data-run-id]').count();
    await history.getByRole('button', { name: '加载更多运行', exact: true }).click();
    await waitUntil(async () => await history.locator('[data-run-id]').count() > before);
  }
  const expectedCount = runs.filter((run) => run.agentId === 'agent-a' && !run.archivedAt).length;
  assert.ok(expectedCount > 200); assert.equal(await history.locator('[data-run-id]').count(), expectedCount);
  const beforeRecovery = agentReads; await page.evaluate(() => window.dispatchEvent(new Event('online')));
  await waitUntil(() => agentReads > beforeRecovery); await page.waitForTimeout(200);
  assert.equal(await history.locator('[data-run-id]').count(), expectedCount, 'automatic recovery keeps loaded pages');
  const search = page.getByRole('searchbox', { name: '搜索运行标题或任务内容' });
  await search.fill('slow'); await waitUntil(() => Boolean(slowHistory));
  await search.fill('%_'); await row('literal-old').waitFor(); slowHistory.release();
  await page.waitForTimeout(300); assert.equal(await history.locator('[data-run-id]').count(), 1);
  assert.equal(new URL(page.url()).pathname, '/orchestrate/runs/run-a'); await assertInput('A newer revision');
  await page.getByLabel('筛选运行状态').selectOption('completed'); await page.getByText('没有符合筛选条件的运行。', { exact: true }).waitFor();
  await page.getByLabel('筛选运行状态').selectOption('failed'); await row('literal-old').waitFor();
  await screenshot('history-server-search');
  await search.fill(''); await page.getByLabel('筛选运行状态').selectOption(''); await row('run-a').waitFor();
  assert.equal(await row('run-active').getByRole('button', { name: '归档运行' }).isDisabled(), true);
  await row('run-a').getByRole('button', { name: '重命名运行' }).click();
  await page.getByLabel('运行标题', { exact: true }).fill('😀'.repeat(201));
  await page.getByRole('button', { name: '保存', exact: true }).click();
  await page.getByRole('alert').filter({ hasText: '标题长度须为 1–200 个字符。' }).waitFor(); assert.equal(patches.length, 0);
  await page.getByLabel('运行标题', { exact: true }).fill('Renamed run A');
  await page.getByRole('button', { name: '保存', exact: true }).click(); await row('run-a').getByText('Renamed run A', { exact: true }).waitFor();
  assert.equal(new URL(page.url()).pathname, '/orchestrate/runs/run-a');
  patchHeld = gate(); const patchCount = patches.length; await row('run-a').getByRole('button', { name: '归档运行' }).click();
  await waitUntil(() => patches.length > patchCount); await select('run-b');
  const heldPatch = patchHeld; patchHeld = undefined; heldPatch.release(); await row('run-a').waitFor({ state: 'detached' });
  assert.equal(new URL(page.url()).pathname, '/orchestrate/runs/run-b'); await assertInput('B edited while A sends');
  await page.getByLabel('筛选归档状态').selectOption('true'); await row('run-a').waitFor(); await select('run-a');
  await assertInput('A newer revision'); await screenshot('history-archived-run');
  // A restore in another tab omits archivedAt from the next detail response.
  // Background recovery must clear that metadata without resetting history.
  const restoredElsewhere = runs.find((run) => run.id === 'run-a');
  delete restoredElsewhere.archivedAt;
  await page.evaluate(() => window.dispatchEvent(new Event('online')));
  await row('run-a').getByRole('button', { name: '归档运行', exact: true }).waitFor();
  await assertInput('A newer revision');
  restoredElsewhere.archivedAt = 1234;
  await page.evaluate(() => window.dispatchEvent(new Event('online')));
  await row('run-a').getByRole('button', { name: '恢复运行', exact: true }).waitFor();
  await row('run-a').getByRole('button', { name: '恢复运行' }).click(); await row('run-a').waitFor({ state: 'detached' });
  await page.getByLabel('筛选归档状态').selectOption('all'); await row('run-a').waitFor();
  await row('run-a').getByRole('button', { name: '归档运行' }).waitFor();
  failHistory = true; await page.getByRole('button', { name: '刷新', exact: true }).click(); await history.getByText('暂时无法加载运行历史。').waitFor();
  failHistory = false; await history.getByRole('button', { name: '重试', exact: true }).click(); await row('run-a').waitFor();

  await page.evaluate(() => {
    const original = Storage.prototype.setItem;
    Storage.prototype.setItem = function (key, value) {
      if (key.startsWith('codexBridge.orchestrationDraft.v1:')) throw new DOMException('Fixture quota', 'QuotaExceededError');
      return original.call(this, key, value);
    };
  });
  await input.fill('quota draft'); await page.getByText('草稿暂存在内存中。浏览器存储不可用，刷新页面可能丢失草稿。', { exact: true }).waitFor();
  await select('run-b'); await select('run-a'); await assertInput('quota draft');
  failPost = true; await continueButton().click(); await page.getByText('Fixture send rejected', { exact: true }).waitFor(); await assertInput('quota draft');
  failPost = false; await continueButton().click(); await assertInput('');
  await input.fill('clear on logout'); await page.getByRole('button', { name: '设置', exact: true }).click();
  await page.getByRole('button', { name: '退出登录', exact: true }).click(); await page.getByLabel('用户名', { exact: true }).waitFor();
  const storage = await page.evaluate(() => Object.fromEntries(Object.entries(localStorage).filter(([key]) => key.startsWith('codexBridge.orchestrationDraft.v1:'))));
  assert.ok(Object.keys(storage).every((key) => !key.startsWith('codexBridge.orchestrationDraft.v1:user-a:')));
  assert.equal(storage['codexBridge.orchestrationDraft.v1:user-b:run:run-a'], 'separate account draft');
  assert.deepEqual(errors, []);
  console.log('desktop workspace browser passed: isolated drafts, refresh, delayed continuation/live input, quota recovery, scoped logout, auth 401/503/network/timeout, server filters, >200-run pagination, rename, archive/restore, stale responses, continuity');
  await page.close();
} catch (error) {
  if (page && !page.isClosed()) {
    if (screenshotDir) await page.screenshot({ path: `${screenshotDir}/workspace-check-failure.png`, fullPage: true }).catch(() => {});
    console.error({ url: page.url(), errors, visibleText: (await page.locator('body').innerText()).slice(0, 8000) });
  }
  throw error;
} finally { await browser.close(); }
