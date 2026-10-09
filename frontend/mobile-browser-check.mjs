import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { chromium } from 'playwright';

// Run only against a local VITE_LEADING=true VITE_OFFICIAL_CODEX_MODELS=true build.
// Every API/WS is mocked; the tests cannot start a real model or modify public data.
const baseURL = process.env.MOBILE_TEST_URL || 'http://127.0.0.1:4203';
assert.ok(['127.0.0.1', 'localhost'].includes(new URL(baseURL).hostname), 'mobile fixtures require a local origin');
const screenshotDir = process.env.MOBILE_SCREENSHOT_DIR;
if (screenshotDir) await mkdir(screenshotDir, { recursive: true });
const wait = async (predicate, message, timeoutMs = 15000) => {
  const deadline = Date.now() + timeoutMs;
  while (!await predicate()) {
    assert.ok(Date.now() < deadline, message || 'fixture did not converge');
    await new Promise((resolve) => setTimeout(resolve, 30));
  }
};
const browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage', '--no-proxy-server'] });
const page = await browser.newPage({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
page.setDefaultTimeout(20000);
const errors = [], writes = [], prompts = [], queries = [], reads = [], frames = [], sockets = new Map(), liveMessages = new Map();
let authenticated = true, administrator = true;
page.on('pageerror', (error) => errors.push(error.message));
const agent = { id: 'mobile-agent', name: 'A very long endpoint name that must stay within the phone width', hostname: 'mobile-fixture', machineId: 'mobile-fixture', online: true, lastSeenAt: 1, workingDirs: ['/tmp/mobile-fixture'], capabilities: {
  approvalPolicy: 'never', sandbox: 'danger-full-access', orchestrationLiveInput: 1,
  orchestration: { codex: { available: true }, claude: { available: true } },
  configSwitcher: { version: 3, clis: ['codex', 'claude'], keyId: 'fixture', publicKey: 'fixture' },
  nativeUserInput: { codex: { available: true, version: 3 }, claude: { available: true, version: 3 } },
} };
const makeRun = (id, extra = {}) => ({ id, agentId: agent.id, title: `Fixture ${id}`, mode: 'collaboration', workerPair: 'codex-codex', firstCli: 'codex', executionMode: 'dynamic', status: 'completed', prompt: 'Fixture task', createdAt: 100, updatedAt: 100, ...extra });
const runs = [makeRun('history-run'), makeRun('delete-run')];
const session = { id: 'mobile-chat', agentId: agent.id, title: 'Existing mobile chat', createdAt: 100, updatedAt: 100 };
const metric = { inputTokens: 20, outputTokens: 5, cacheTokens: 0, totalTokens: 25, callCount: 1, estimatedCostUsd: .1, costKnown: true };
const adminUser = { ...metric, userId: 'mobile-user', username: 'fixture', createdAt: 1, lastActiveAt: 100,
  activityStatus: 'online', onlineAgents: 1, totalAgents: 1, chatSessions: 1, orchestrationRuns: 1, runningRuns: 1 };
const eventsFor = (id) => [{ runId: id, seq: 1, kind: 'turn.delta', source: 'cli', cli: 'codex', turnId: 'mobile-turn',
  role: 'agent', content: 'Mobile fixture worker output', createdAt: 100 }];
const progressFor = (id) => {
  const planItems = [{ id: 'mobile-plan', title: 'Mobile fixture plan item', status: 'in_progress', kind: 'implementation' }];
  const graph = { id: 'mobile-graph', runId: id, generation: 1, status: 'running', parallelLimit: 1, payloadDigest: 'fixture', createdAt: 1, updatedAt: 1,
    tasks: [{ id: 'mobile-node', name: 'execute', role: 'worker', workerSlot: 'codex-a', status: 'running', position: 1, dependencies: [], createdAt: 1, updatedAt: 1 }] };
  const plan = { items: planItems, total: 1, completed: 0, percent: 0, currentFocus: 'mobile-plan' };
  return { graph, planItems, plan, tasks: [{ taskNumber: 1, prompt: 'Mobile fixture plan prompt', graphs: [graph], graph, planItems, plan }] };
};
await page.addInitScript(() => {
  localStorage.setItem('codexBridge.language', 'zh'); localStorage.setItem('codexBridge.selectedAgentId', 'mobile-agent');
});
await page.route('**/api/**', async (route) => {
  const request = route.request(), url = new URL(request.url()), path = url.pathname, method = request.method();
  let data = {};
  if (method === 'GET') reads.push(path + url.search);
  if (method !== 'GET') writes.push({ path, method, body: request.postData() ? request.postDataJSON() : undefined });
  if (path === '/api/me') {
    if (!authenticated) return route.fulfill({ status: 401, json: {} });
    data = { nativeUserInputProtocolVersion: 3, user: { id: 'mobile-user', username: 'fixture', isAdmin: administrator, createdAt: 1,
      features: ['official-codex-worker-models', 'native-user-input', 'orchestration-plan-workspace'] } };
  }
  else if (path === '/api/auth/config') data = { registrationEnabled: true };
  else if (path === '/api/agents') data = { agents: [agent] };
  else if (path.endsWith('/official-models')) data = { models: [
    { model: 'reasoner', displayName: 'Reasoner', supportedReasoningEfforts: ['low', 'high'] },
    { model: 'fast', displayName: 'Fast', supportedReasoningEfforts: ['minimal', 'low'] },
  ] };
  else if (path.endsWith('/presets')) data = { presets: [] };
  else if (path.endsWith('/official-reset')) data = {};
  else if (path === '/api/orchestrations' && method === 'POST') {
    const run = makeRun('new-mobile-run', request.postDataJSON()); runs.push(run); data = { run };
  } else if (path === '/api/orchestrations') {
    queries.push(url.searchParams.toString());
    data = { runs: runs.filter((run) => (!url.searchParams.get('q') || run.title.includes(url.searchParams.get('q')))
      && (!url.searchParams.get('status') || run.status === url.searchParams.get('status'))
      && (url.searchParams.get('archived') === 'all' || Boolean(run.archivedAt) === (url.searchParams.get('archived') === 'true'))) };
  }
  else if (path.endsWith('/prompts') && method === 'POST') {
    const id = path.split('/')[3], body = request.postDataJSON();
    if (body.messageId) {
      const list = liveMessages.get(id) || [];
      list.push({ ...body, id: body.messageId, runId: id, status: 'queued', createdAt: 100, updatedAt: 100 });
      liveMessages.set(id, list); data = { message: list.at(-1) };
    } else { const run = runs.find((run) => run.id === id); run.status = 'running'; data = { run }; }
  }
  else if (path.endsWith('/prompts')) data = { messages: liveMessages.get(path.split('/')[3]) || [] };
  else if (path.endsWith('/events')) data = { events: eventsFor(path.split('/')[3]), hasMoreBefore: false, hasMoreAfter: false };
  else if (path.endsWith('/progress')) data = progressFor(path.split('/')[3]);
  else if (path.endsWith('/stats')) data = { stats: { ...metric, runId: path.split('/')[3], runtimeSeconds: 50, byCLI: [], byWorker: [], rounds: [], tasks: [] } };
  else if (/^\/api\/orchestrations\/[^/]+$/.test(path)) {
    const run = runs.find((item) => path.endsWith(item.id));
    if (method === 'PATCH') {
      const body = request.postDataJSON();
      if (body.title !== undefined) run.title = body.title;
      if (body.archived !== undefined) { if (body.archived) run.archivedAt = 100; else delete run.archivedAt; }
    }
    if (method === 'DELETE') runs.splice(runs.indexOf(run), 1);
    data = { run };
  } else if (path.endsWith('/share')) data = { share: { id: 'fixture-share', url: `${baseURL}/share/fixture-share` } };
  else if (path === '/api/sessions') data = { sessions: [session] };
  else if (path.endsWith('/messages')) data = { messages: [] };
  else if (path.startsWith('/api/sessions/')) data = { session };
  else if (path === '/api/usage/overview') data = { overview: { ...metric, days: Number(url.searchParams.get('days')), runs: 1, machines: 1, trend: [],
    items: [{ ...metric, runId: 'new-mobile-run', title: 'Mobile overview result', machineId: 'fixture', machineName: 'Mobile fixture', createdAt: 1, status: 'running', byCLI: [] }] },
    page: { scannedRuns: 1, hasMore: false } };
  else if (path === '/api/admin/usage') data = { overview: { ...metric, ...adminUser, days: 30, users: 1, activeUsers: 1, onlineUsers: 1, trend: [], items: [adminUser] } };
  else if (path.endsWith('/usage') && path.startsWith('/api/admin/users/')) data = { detail: { ...metric, userId: 'mobile-user', username: 'fixture', createdAt: 1, days: 30,
    conversations: [{ ...metric, id: session.id, kind: 'chat', title: 'Mobile admin conversation', agentName: 'Fixture', status: 'completed', activityCount: 1, createdAt: 1, updatedAt: 1 }] } };
  else if (path.startsWith('/api/admin/users/')) data = { conversation: { id: session.id, kind: 'chat', title: 'Mobile admin conversation',
    items: [{ role: 'user', kind: 'message', content: 'Mobile admin read-only content', createdAt: 1 }], hasMoreBefore: false } };
  else if (path.startsWith('/api/public/shares/')) data = { share: { id: 'fixture-share', title: 'Mobile public share', kind: 'orchestration' },
    run: makeRun('new-mobile-run'), events: eventsFor('new-mobile-run') };
  await route.fulfill({ json: data }).catch(() => {});
});
await page.routeWebSocket('**/ws/**', (socket) => {
  const id = new URL(socket.url()).searchParams.get('runId');
  if (id) sockets.set(id, socket);
  socket.onMessage((raw) => {
    const frame = JSON.parse(raw); frames.push({ runId: id, socket, ...frame });
    if (frame.type === 'prompt') prompts.push(frame);
    if (frame.type === 'user_input_sync') socket.send(JSON.stringify({ type: 'user_input_snapshot', payload: {
      runId: id, syncId: frame.payload.syncId, instanceId: 'mobile-instance', revision: 0, requests: [], statuses: [], receipts: [], closed: [],
    } }));
  });
});
const screen = async (name) => { if (screenshotDir) await page.screenshot({ path: `${screenshotDir}/${name}.png` }); };
const fits = async () => {
  const dims = await page.evaluate(() => ({ width: innerWidth, body: document.documentElement.scrollWidth }));
  assert.ok(dims.body <= dims.width + 1, `page overflow: ${JSON.stringify(dims)}`);
};
const setup = () => page.getByRole('dialog', { name: '初始化任务', exact: true });
const openSetup = async () => { await page.getByRole('button', { name: '初始化任务', exact: true }).click(); await setup().waitFor(); };
const targetSize = async (locator, label) => {
  await locator.waitFor({ state: 'visible' });
  const bounds = await locator.boundingBox();
  assert.ok(bounds && bounds.width >= 48 && bounds.height >= 48, `${label} target: ${JSON.stringify(bounds)}`);
};
const openHistory = () => page.getByRole('button', { name: '运行历史', exact: true }).first().click();
const openMore = () => page.getByRole('button', { name: '更多操作', exact: true }).click();
const openConversation = async () => {
  await page.getByRole('button', { name: '运行对话', exact: true }).click();
  await page.getByRole('dialog', { name: '运行对话', exact: true }).waitFor();
};
try {
  await page.goto(`${baseURL}/orchestrate`, { waitUntil: 'domcontentloaded' });
  await wait(async () => await page.locator('select[aria-label="选择 CLI 端"]:visible').count(), 'local phone fixture did not load', 60000);
  await fits(); await screen('01-orchestration');
  const readHeight = await page.locator('.phone-transcript').evaluate((node) => node.getBoundingClientRect().height);
  assert.ok(readHeight > 400, `the transcript owns phone reading space, measured ${readHeight}`);
  await openSetup();
  await setup().getByRole('button', { name: 'Codex + Codex', exact: true }).click();
  const modelA = setup().getByLabel('Codex A 模型', { exact: true }), modelB = setup().getByLabel('Codex B 模型', { exact: true });
  await wait(async () => !await modelA.isDisabled());
  await modelA.selectOption('reasoner'); await setup().getByLabel('Codex A 思考强度', { exact: true }).selectOption('high');
  await modelB.selectOption('fast'); await setup().getByLabel('Codex B 思考强度', { exact: true }).selectOption('minimal');
  await setup().locator('textarea').fill('Phone draft survives closing, rotating, and opening history');
  await setup().locator('input[type="file"]').setInputFiles({ name: 'fixture.txt', mimeType: 'text/plain', buffer: Buffer.from('phone attachment') });
  await screen('02-initialize');
  await page.keyboard.press('Escape');
  assert.equal(await setup().count(), 0, 'Escape closes presentation');
  assert.equal(writes.length, 0, 'opening and closing setup does not submit a task or reset login');
  for (const viewport of [{ width: 360, height: 800 }, { width: 412, height: 915 }, { width: 844, height: 390 }, { width: 390, height: 400 }]) {
    await page.setViewportSize(viewport); await fits();
    await targetSize(page.locator('select[aria-label="选择 CLI 端"]:visible'), `CLI select ${viewport.width}x${viewport.height}`);
    await openSetup();
    assert.equal(await setup().locator('textarea').inputValue(), 'Phone draft survives closing, rotating, and opening history');
    assert.equal(await modelA.inputValue(), 'reasoner'); assert.equal(await modelB.inputValue(), 'fast');
    assert.ok(await setup().getByText('fixture.txt', { exact: true }).count());
    await page.keyboard.press('Escape');
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: '运行历史', exact: true }).first().click();
  const history = page.getByRole('dialog', { name: '运行历史', exact: true });
  await history.waitFor();
  await history.getByRole('searchbox').fill('history-run');
  await wait(() => queries.some((query) => query.includes('q=history-run')));
  await history.getByRole('searchbox').fill('');
  await screen('03-history');
  // Exercise the original mutation callbacks against fixture history, not public data.
  const historyRow = history.locator('[data-run-id="history-run"]');
  await targetSize(historyRow.getByRole('button', { name: '重命名运行', exact: true }), 'run rename');
  await historyRow.getByRole('button', { name: '重命名运行', exact: true }).click();
  const renameRun = page.getByRole('dialog', { name: '重命名运行', exact: true });
  await renameRun.getByRole('textbox').fill('Mobile renamed history');
  await renameRun.getByRole('button', { name: '保存', exact: true }).click();
  await wait(() => writes.some((write) => write.path.endsWith('/history-run') && write.body?.title === 'Mobile renamed history'));
  await historyRow.getByText('Mobile renamed history', { exact: true }).waitFor();
  await history.locator('select').nth(1).selectOption('all');
  await historyRow.getByRole('button', { name: '归档运行', exact: true }).click();
  await historyRow.getByRole('button', { name: '恢复运行', exact: true }).waitFor();
  assert.ok(writes.some((write) => write.path.endsWith('/history-run') && write.body?.archived === true));
  await historyRow.getByRole('button', { name: '恢复运行', exact: true }).click();
  await historyRow.getByRole('button', { name: '归档运行', exact: true }).waitFor();
  assert.ok(writes.some((write) => write.path.endsWith('/history-run') && write.body?.archived === false));
  page.on('dialog', (dialog) => dialog.accept());
  await history.locator('[data-run-id="delete-run"]').getByRole('button', { name: '删除运行', exact: true }).click();
  await wait(() => writes.some((write) => write.path.endsWith('/delete-run') && write.method === 'DELETE'));
  await wait(async () => await history.locator('[data-run-id="delete-run"]').count() === 0);
  await page.keyboard.press('Escape');
  assert.equal(await history.count(), 0, 'Escape closes history after deleting its focused row');
  await openSetup();
  // Browser text enlargement, without changing business state.
  await page.evaluate(() => {
    const root = document.documentElement;
    root.style.fontSize = `${parseFloat(getComputedStyle(root).fontSize) * 2}px`;
  }); await fits(); await screen('04-large-text');
  await page.evaluate(() => document.documentElement.style.fontSize = '');
  for (const preset of await setup().locator('select[aria-label$="工作器模型预设"]').all()) {
    const confirm = preset.locator('..').getByRole('button', { name: '确认', exact: true });
    if (await confirm.count()) await confirm.click();
  }
  await wait(async () => await setup().getByRole('button', { name: '开始', exact: true }).isEnabled());
  await setup().getByRole('button', { name: '开始', exact: true }).click();
  await wait(() => writes.some((write) => write.path === '/api/orchestrations'));
  const initial = writes.find((write) => write.path === '/api/orchestrations').body;
  assert.deepEqual(initial.workerProfileModels, { 'codex-a': 'reasoner', 'codex-b': 'fast' });
  assert.deepEqual(initial.workerProfileEfforts, { 'codex-a': 'high', 'codex-b': 'minimal' });
  assert.equal(initial.files[0].name, 'fixture.txt');
  await wait(async () => new URL(page.url()).pathname.endsWith('/new-mobile-run'));
  await openConversation();
  await page.locator('#orchestration-live-input').fill('Continue on the existing run');
  await page.getByRole('button', { name: '继续运行', exact: true }).click();
  await wait(() => writes.some((write) => write.path === '/api/orchestrations/new-mobile-run/prompts'));
  assert.equal(writes.filter((write) => write.path === '/api/orchestrations').length, 1, 'follow-up must preserve runID');
  const live = page.getByRole('region', { name: '运行对话', exact: true });
  await live.getByRole('textbox', { name: '追加指令' }).fill('Phone queued follow-up');
  await live.getByRole('button', { name: '排队发送', exact: true }).click();
  await live.getByText('Phone queued follow-up', { exact: true }).waitFor();
  const queued = writes.find((write) => write.body?.prompt === 'Phone queued follow-up');
  assert.equal(queued.path, '/api/orchestrations/new-mobile-run/prompts');
  assert.equal(queued.body.deliveryMode, 'queue'); assert.ok(queued.body.messageId);
  await live.getByRole('textbox', { name: '追加指令' }).fill('Phone immediate follow-up');
  await live.getByRole('button', { name: '立即发送', exact: true }).click();
  await live.getByText('Phone immediate follow-up', { exact: true }).waitFor();
  const immediate = writes.find((write) => write.body?.prompt === 'Phone immediate follow-up');
  assert.equal(immediate.path, queued.path); assert.equal(immediate.body.deliveryMode, 'immediate');
  await live.getByRole('textbox', { name: '追加指令' }).fill('Short-screen live draft stays in the original run');
  await page.keyboard.press('Escape');
  for (const viewport of [{ width: 844, height: 390 }, { width: 390, height: 400 }]) {
    await page.setViewportSize(viewport); await fits();
    const height = await page.locator('.phone-transcript').evaluate((node) => node.getBoundingClientRect().height);
    assert.ok(height >= 120, `running transcript remains readable at ${viewport.width}x${viewport.height}: ${height}`);
    await targetSize(page.getByRole('button', { name: '运行对话', exact: true }), 'run conversation entry');
    await screen(`21-reading-${viewport.width}x${viewport.height}`);
    await openConversation(); await fits();
    const input = live.getByRole('textbox', { name: '追加指令' });
    assert.equal(await input.inputValue(), 'Short-screen live draft stays in the original run');
    await live.getByText('Phone immediate follow-up', { exact: true }).waitFor();
    await input.scrollIntoViewIfNeeded();
    const bounds = await input.boundingBox();
    assert.ok(bounds && bounds.y >= 0 && bounds.y + bounds.height <= viewport.height, 'short-screen composer input is reachable');
    const queueButton = live.getByRole('button', { name: '排队发送', exact: true });
    await queueButton.scrollIntoViewIfNeeded();
    await targetSize(queueButton, 'short-screen queue button');
    const queueBounds = await queueButton.boundingBox();
    assert.ok(queueBounds && queueBounds.y >= 0 && queueBounds.y + queueBounds.height <= viewport.height, 'short-screen queue button is reachable');
    await screen(`22-composer-${viewport.width}x${viewport.height}`);
    await page.keyboard.press('Escape');
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await openConversation();
  assert.equal(await live.getByRole('textbox', { name: '追加指令' }).inputValue(), 'Short-screen live draft stays in the original run');
  assert.equal(writes.filter((write) => write.path === '/api/orchestrations').length, 1, 'composer panels preserve the original run');
  await page.keyboard.press('Escape');
  // Wait until the active socket has completed its native-input handshake.
  await wait(() => frames.some((frame) => frame.type === 'user_input_sync' && frame.runId === 'new-mobile-run' && frame.socket === sockets.get('new-mobile-run')));
  const socket = sockets.get('new-mobile-run');
  const nativeRequest = { requestId: 'mobile-question', runId: 'new-mobile-run', executionId: 'mobile-execution', instanceId: 'mobile-instance',
    cli: 'codex', workerId: 'codex-a', blocking: true, revision: 1,
    questions: [{ id: 'mobile-choice', question: 'Mobile fixture decision', selectionMode: 'single', allowCustom: true,
      options: [{ id: 'a', label: 'Phone option A' }, { id: 'b', label: 'Phone option B' }] }] };
  socket.send(JSON.stringify({ type: 'user_input_request', payload: nativeRequest }));
  const question = page.locator('#native-user-input-mobile-question');
  await question.getByRole('radio', { name: 'Phone option B', exact: true }).check();
  await openHistory(); await page.keyboard.press('Escape');
  assert.ok(await question.getByRole('radio', { name: 'Phone option B', exact: true }).isChecked(), 'history dismissal keeps pending answer');
  await question.getByRole('button', { name: '提交回答', exact: true }).click();
  await wait(() => frames.some((frame) => frame.type === 'user_input_response'));
  const answer = frames.find((frame) => frame.type === 'user_input_response');
  assert.equal(answer.runId, nativeRequest.runId); assert.equal(answer.payload.runId, nativeRequest.runId);
  assert.deepEqual(answer.payload.answers, { 'mobile-choice': { optionIds: ['b'] } });
  socket.send(JSON.stringify({ type: 'user_input_status', payload: { ...nativeRequest, questions: undefined,
    submissionId: answer.payload.submissionId, status: 'closed', lifecycle: 'closed', revision: 2 } }));
  socket.send(JSON.stringify({ type: 'approval_request', payload: { requestId: 'mobile-approval', runId: nativeRequest.runId,
    kind: 'command', command: 'echo mobile-fixture', reason: 'Mobile fixture approval' } }));
  await page.getByRole('button', { name: '允许', exact: true }).click();
  await wait(() => frames.some((frame) => frame.type === 'approval_response'));
  assert.deepEqual(frames.find((frame) => frame.type === 'approval_response').payload, { requestId: 'mobile-approval', decision: 'accept' });
  assert.equal(frames.filter((frame) => frame.type === 'user_input_response').length, 1, 'panel changes cannot resend answers');
  await screen('08-live-and-approval');
  // Completed question entries stay clickable while the long timeline's shortcut is visible.
  const completedRequests = Array.from({ length: 3 }, (_, index) => ({ ...nativeRequest,
    requestId: `mobile-completed-${index + 1}`, blocking: false,
    questions: [{ ...nativeRequest.questions[0], id: `completed-choice-${index + 1}` }],
  }));
  for (const request of completedRequests) socket.send(JSON.stringify({ type: 'user_input_request', payload: request }));
  for (const [index, request] of completedRequests.entries()) {
    const card = page.locator(`#native-user-input-${request.requestId}`);
    await card.getByRole('radio', { name: 'Phone option B', exact: true }).check();
    await card.getByRole('button', { name: index < 2 ? '提交并继续下一题' : '提交回答', exact: true }).click();
    await wait(() => frames.some((frame) => frame.type === 'user_input_response' && frame.payload.requestId === request.requestId));
    const response = frames.find((frame) => frame.type === 'user_input_response' && frame.payload.requestId === request.requestId);
    assert.equal(response.runId, nativeRequest.runId);
    socket.send(JSON.stringify({ type: 'user_input_status', payload: { ...request, questions: undefined,
      submissionId: response.payload.submissionId, status: 'delivered', revision: 2 } }));
  }
  const completedEntry = page.getByRole('button', { name: /问答已完成.*3 题已送达.*查看记录/ });
  await completedEntry.waitFor();
  for (let index = 0; index < 48; index++) socket.send(JSON.stringify({ type: 'orchestration_event', payload: {
    runId: nativeRequest.runId, seq: index + 2, kind: 'turn.delta', source: 'cli', cli: 'codex', role: 'agent',
    content: `Mobile timeline ${index + 1}: ${'long output '.repeat(12)}`, createdAt: index + 200,
  } }));
  await wait(async () => await page.locator('.phone-transcript').evaluate((node) => node.scrollHeight > node.clientHeight + 100));
  await page.locator('.phone-transcript').evaluate((node) => { node.scrollTop = 0; node.dispatchEvent(new Event('scroll', { bubbles: true })); });
  const jump = page.locator('.phone-jump-latest');
  await jump.waitFor(); await targetSize(jump, 'jump to latest');
  assert.ok(await completedEntry.evaluate((node) => {
    const rect = node.getBoundingClientRect();
    return node.contains(document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2));
  }), 'completed question entry center is not covered by the timeline shortcut');
  await screen('23-completed-question-entry');
  await completedEntry.click();
  await page.locator('#native-user-input-panel').waitFor({ state: 'visible' });
  assert.equal(frames.filter((frame) => frame.type === 'user_input_response').length, 4, 'completed-entry navigation never resends answers');
  await page.locator('#native-user-input-panel').getByRole('button', { name: '收起', exact: true }).click();
  await page.locator('#native-user-input-panel').waitFor({ state: 'hidden' });
  await page.getByRole('button', { name: '打开规划进度', exact: true }).click();
  const planning = page.getByRole('dialog', { name: '规划进度', exact: true });
  await planning.getByText('Mobile fixture plan item', { exact: true }).first().waitFor();
  await fits(); await screen('09-planning');
  await planning.getByRole('button', { name: '放大执行链', exact: true }).click();
  const graph = page.getByRole('dialog', { name: '任务 1 · Agent 执行链', exact: true });
  await graph.getByRole('img', { name: '可缩放的 Agent 执行链', exact: true }).waitFor();
  await targetSize(graph.getByRole('button', { name: '放大', exact: true }), 'graph zoom');
  await graph.getByRole('button', { name: '放大', exact: true }).click();
  await fits(); await screen('10-graph'); await page.keyboard.press('Escape');
  await graph.waitFor({ state: 'hidden' });
  await planning.getByRole('button', { name: '展开任务分支地图', exact: true }).click();
  const taskMap = page.getByRole('dialog', { name: '任务 1 · 任务分支地图', exact: true });
  await taskMap.getByRole('img', { name: '可缩放的任务分支地图', exact: true }).waitFor();
  await fits(); await screen('11-task-map'); await page.keyboard.press('Escape');
  await taskMap.waitFor({ state: 'hidden' });
  await page.keyboard.press('Escape'); await planning.waitFor({ state: 'hidden' });
  await page.getByRole('button', { name: '更多操作', exact: true }).click();
  const more = page.getByRole('dialog', { name: '更多操作', exact: true });
  for (const name of ['运行统计', '用量总览', '管理员总览', '分享运行']) assert.equal(await more.getByRole('button', { name, exact: true }).count(), 1, `${name} is reachable`);
  await screen('05-more'); await page.keyboard.press('Escape');
  await page.getByRole('button', { name: '运行历史', exact: true }).first().click();
  await history.getByRole('button', { name: '设置', exact: true }).click();
  const settings = page.getByRole('dialog', { name: '设置', exact: true });
  await settings.waitFor();
  await settings.getByRole('button', { name: '模型库', exact: true }).click();
  const library = page.getByRole('dialog', { name: '模型库', exact: true });
  await library.waitFor(); await fits(); await screen('12-model-library');
  await settings.getByRole('button', { name: '模型库', exact: true }).evaluate((button) => button.focus());
  await page.keyboard.press('Escape'); await library.waitFor({ state: 'hidden' });
  assert.ok(await settings.isVisible(), 'Escape closes the foremost library and preserves Settings');
  await fits(); await screen('06-settings');
  await page.evaluate(() => { document.body.tabIndex = -1; document.body.focus(); document.body.removeAttribute('tabindex'); });
  await page.keyboard.press('Escape');
  await settings.waitFor({ state: 'hidden' });
  assert.ok(await history.isVisible(), 'closing Settings preserves the background history panel');
  await page.keyboard.press('Escape'); await history.waitFor({ state: 'hidden' });
  // Follow each visible navigation action through the real page and its read APIs.
  await openMore(); await more.getByRole('button', { name: '分享运行', exact: true }).click();
  await wait(() => writes.some((write) => write.path === '/api/orchestrations/new-mobile-run/share'));
  await more.getByRole('button', { name: '运行统计', exact: true }).click();
  await page.getByRole('heading', { name: '运行统计', exact: true }).waitFor();
  await wait(() => reads.some((read) => read === '/api/orchestrations/new-mobile-run/stats'));
  await fits(); await screen('13-run-statistics');
  await page.goto(`${baseURL}/orchestrate/runs/new-mobile-run`, { waitUntil: 'domcontentloaded' });
  await openMore(); await more.getByRole('button', { name: '用量总览', exact: true }).click();
  await page.getByText('Mobile overview result', { exact: true }).waitFor();
  await fits(); await screen('14-usage-overview');
  await page.getByRole('button', { name: '近 7 天', exact: true }).click();
  await wait(() => reads.some((read) => read.startsWith('/api/usage/overview?') && read.includes('days=7')));
  await page.goto(`${baseURL}/orchestrate/runs/new-mobile-run`, { waitUntil: 'domcontentloaded' });
  await openMore(); await more.getByRole('button', { name: '管理员总览', exact: true }).click();
  const readOnlyWriteCount = writes.length;
  await page.getByRole('button', { name: '查看用户详情: fixture', exact: true }).click();
  await page.getByText('Mobile admin conversation', { exact: true }).filter({ visible: true }).first().waitFor();
  await fits(); await screen('15-admin-user');
  await page.getByRole('button', { name: /Mobile admin conversation/ }).filter({ visible: true }).click();
  const contentSheet = page.getByRole('dialog', { name: 'Mobile admin conversation', exact: true });
  await contentSheet.getByText('Mobile admin read-only content', { exact: true }).waitFor();
  await fits(); await screen('16-admin-content'); await page.keyboard.press('Escape');
  assert.equal(writes.length, readOnlyWriteCount, 'administrator content viewing stays read-only');
  await page.goto(`${baseURL}/share/fixture-share`, { waitUntil: 'domcontentloaded' });
  await page.getByText('Mobile public share', { exact: true }).filter({ visible: true }).waitFor();
  await page.getByText('Mobile fixture worker output', { exact: true }).waitFor();
  await fits(); await screen('17-public-share');
  assert.equal(writes.length, readOnlyWriteCount, 'public share is read-only');
  await page.goto(baseURL, { waitUntil: 'domcontentloaded' });
  await page.getByRole('button', { name: '会话历史', exact: true }).click();
  const chats = page.getByRole('dialog', { name: '会话历史', exact: true });
  const renameButton = chats.getByRole('button', { name: '重命名会话', exact: true });
  const renameBounds = await renameButton.boundingBox();
  assert.ok(renameBounds.width >= 48 && renameBounds.height >= 48, `phone history target: ${JSON.stringify(renameBounds)}`);
  await renameButton.click();
  const rename = page.getByRole('dialog', { name: '重命名会话', exact: true });
  await rename.getByRole('textbox').fill('Dismissed mobile rename');
  await page.keyboard.press('Escape');
  assert.equal(await rename.count(), 0, 'Escape dismisses the foreground rename dialog');
  assert.ok(await chats.isVisible(), 'dismissal keeps the background history available');
  assert.equal(writes.filter((write) => write.path === `/api/sessions/${session.id}` && write.method === 'PATCH').length, 0, 'dismissal does not save a rename');
  await chats.getByRole('button', { name: 'Existing mobile chat', exact: true }).click();
  const chatInput = page.locator('textarea:visible').first(); await chatInput.fill('Same sid on a phone');
  await page.locator('input[type="file"]').setInputFiles({ name: 'phone.png', mimeType: 'image/png',
    buffer: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aFOsAAAAASUVORK5CYII=', 'base64') });
  await page.getByRole('img', { name: 'phone.png', exact: true }).waitFor();
  await page.getByRole('button', { name: '会话历史', exact: true }).click(); await page.keyboard.press('Escape');
  assert.equal(await chatInput.inputValue(), 'Same sid on a phone');
  await page.getByRole('button', { name: '发送', exact: true }).click();
  await wait(() => prompts.length === 1); assert.equal(prompts[0].sid, session.id);
  assert.equal(prompts[0].payload.attachments[0].name, 'phone.png', 'chat attachment follows the original prompt payload');
  assert.equal(writes.filter((write) => write.path === '/api/sessions' && write.method === 'POST').length, 0);
  await fits(); await screen('07-chat');
  await openMore();
  await more.getByRole('button', { name: '分享对话', exact: true }).click();
  await wait(() => writes.some((write) => write.path === `/api/sessions/${session.id}/share`));
  await page.keyboard.press('Escape');
  await page.setViewportSize({ width: 1440, height: 960 });
  assert.equal(await page.getByRole('button', { name: '会话历史', exact: true }).filter({ visible: true }).count(), 0);
  assert.equal(await page.locator('.mobile-surface').first().isVisible(), true, 'desktop sidebar remains available');
  // Permission-bound navigation and public help/login remain useful at phone sizes.
  await page.setViewportSize({ width: 390, height: 844 }); administrator = false;
  await page.goto(baseURL, { waitUntil: 'domcontentloaded' });
  await openMore();
  assert.equal(await more.getByRole('button', { name: '管理员总览', exact: true }).count(), 0, 'regular account has no admin action');
  await page.keyboard.press('Escape');
  await page.goto(`${baseURL}/help`, { waitUntil: 'domcontentloaded' });
  await page.getByRole('button', { name: '打开目录', exact: true }).click();
  const contents = page.getByRole('dialog', { name: '使用目录', exact: true });
  await contents.waitFor(); await fits(); await screen('18-help');
  await page.keyboard.press('Escape'); await contents.waitFor({ state: 'hidden' });
  await page.goto(`${baseURL}/updates`, { waitUntil: 'domcontentloaded' });
  await page.getByRole('heading', { name: '更新记录与版本信息', exact: true }).waitFor();
  await fits(); await screen('19-updates');
  authenticated = false;
  await page.goto(baseURL, { waitUntil: 'domcontentloaded' });
  await page.getByRole('tab', { name: '登录', exact: true }).waitFor();
  await targetSize(page.getByRole('tab', { name: '登录', exact: true }), 'login tab');
  await page.getByLabel('用户名', { exact: true }).fill('mobile-fixture');
  await page.getByLabel('密码', { exact: true }).fill('local-fixture-password');
  await targetSize(page.getByRole('button', { name: '显示密码', exact: true }), 'password visibility');
  await page.getByRole('button', { name: '显示密码', exact: true }).click();
  assert.equal(await page.locator('#password').getAttribute('type'), 'text');
  await page.getByRole('tab', { name: '注册账户', exact: true }).click();
  await page.locator('#confirmPassword').waitFor();
  await fits(); await screen('20-auth');
  assert.equal(writes.filter((write) => ['/api/login', '/api/register'].includes(write.path)).length, 0, 'auth presentation test never submits credentials');
  assert.equal(errors.length, 0, errors.join('\n'));
  console.log('mobile-browser-check PASS: phone sizes/rotation/short viewport/text zoom; draft/model/file continuity; first A/B payload and same run/sid; active queue/immediate inputs; native question/approval; planning/maps; history rename/archive/restore/delete; chat image/share; statistics/usage/admin content/public share; Settings/model library; permissions/help/updates/auth; desktop presentation');
} catch (error) {
  await screen('99-failure');
  console.error('Local mobile fixture failure:', { url: page.url(), errors, body: (await page.locator('body').innerText()).slice(0, 12000) });
  throw error;
} finally { await browser.close(); }
