import assert from 'node:assert/strict';
import { chromium } from 'playwright';

const waitForState = async (predicate) => {
  const deadline = Date.now() + 5000;
  while (!predicate()) {
    assert.ok(Date.now() < deadline, 'mock transport state did not converge');
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
};

const baseURL = process.env.USER_INPUT_TEST_URL || 'http://127.0.0.1:4199';
const run = { id: 'input-fixture', agentId: 'agent-fixture', title: 'Native input fixture', mode: 'collaboration',
  prompt: 'Fixture', maxTurns: 2, status: 'running', createdAt: 1, updatedAt: 1 };
const events = Array.from({ length: 48 }, (_, index) => ({ runId: run.id, seq: index + 1,
  kind: 'turn.delta', source: 'cli', cli: 'codex', role: 'agent', content: `Log entry ${index + 1}: ${'long output '.repeat(12)}`,
  createdAt: index + 1 }));
for (const [index, slot] of ['codex-a', 'codex-b', 'claude-a', 'claude-b'].entries()) {
  events[index].cli = slot.startsWith('codex') ? 'codex' : 'claude';
  events[index].turnId = `identity-${slot}`;
  events[index].task = { workerSlot: slot, name: index % 2 ? 'review' : 'candidate-b' };
}
const request = (id, blocking, secret = false) => ({ requestId: id, runId: run.id, executionId: 'exec-fixture',
  instanceId: 'bridge-fixture', cli: secret ? 'claude' : 'codex', workerId: secret ? 'claude-b' : 'codex-a', blocking, revision: 1,
  questions: [{ id: `q-${id}`, question: secret ? 'Private token' : `Decision ${id}`, selectionMode: 'single',
    secret, allowCustom: true, options: secret ? [] : [{ id: 'a', label: 'A' }, { id: 'b', label: 'B' }] }] });

const browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
try {
  for (const width of [1280, 390]) {
    const page = await browser.newPage({ viewport: { width, height: 844 } });
    page.setDefaultTimeout(60000);
    const errors = [];
    let socket;
    let runStatus = 'running';
    let connectionCount = 0;
    let submission;
    let heldSubmission;
    let fenceSync;
    let recoveryMode = '';
    const recoveryAnswers = [];
    let terminalSocketClosed = false;
    const terminalFrames = [];
    let featureEnabled = true;
    let hubProtocolVersion = 3;
    page.on('pageerror', (error) => errors.push(error.message));
    await page.addInitScript(() => {
      localStorage.setItem('codexBridge.language', 'zh');
    });
    await page.route('**/api/**', async (route) => {
      const url = new URL(route.request().url());
      let data = {};
      if (url.pathname === '/api/me') data = { nativeUserInputProtocolVersion: hubProtocolVersion, user: { id: 'user-fixture', username: 'fixture', createdAt: 1, features: featureEnabled ? ['native-user-input'] : [] } };
      else if (url.pathname === '/api/agents') data = { agents: [{ id: run.agentId, name: 'Fixture Bridge', machineId: 'fixture', hostname: 'fixture', lastSeenAt: 1, online: true,
        capabilities: { orchestrationLiveInput: 1, orchestration: { codex: { available: true }, claude: { available: true } }, nativeUserInput: { codex: { available: true, version: 3 }, claude: { available: true, version: 3 } } } }] };
      else if (url.pathname === '/api/orchestrations') data = { runs: [{ ...run, status: runStatus }] };
      else if (url.pathname.endsWith('/prompts')) data = { messages: [] };
      else if (url.pathname.endsWith('/events')) data = { events, hasMoreBefore: false };
      else if (url.pathname === `/api/orchestrations/${run.id}`) data = { run: { ...run, status: runStatus } };
      else if (url.pathname.includes('/presets')) data = { presets: [] };
      await route.fulfill({ json: data });
    });
    await page.routeWebSocket('**/ws/orchestrations?runId=input-fixture', (ws) => {
      socket = ws;
      connectionCount += 1;
      const terminalConnection = runStatus === 'completed';
      ws.onClose(() => { if (terminalConnection) terminalSocketClosed = true; });
      ws.onMessage((message) => {
        const frame = JSON.parse(message);
        if (terminalConnection) terminalFrames.push(frame.type);
        if (frame.type === 'user_input_response') {
          submission = frame.payload;
          if (frame.payload.requestId === 'refresh') {
            recoveryAnswers.push(frame.payload);
            if (!heldSubmission) { heldSubmission = frame.payload; recoveryMode = 'hold'; }
          }
        }
        if (frame.type === 'user_input_sync') {
          assert.equal(frame.payload.clientVersion, 3, 'browser must advertise receipt-capable protocol');
          if (recoveryMode) {
            if (recoveryMode === 'hold') return;
            if (recoveryMode === 'query' && frame.payload.pendingSubmissions?.length) { fenceSync = frame.payload; return; }
            ws.send(JSON.stringify({ type: 'user_input_snapshot', payload: {
              runId: run.id, syncId: frame.payload.syncId, instanceId: 'bridge-fixture', revision: 10,
              requests: [{ ...request('refresh', true), revision: 10 }], statuses: [], receipts: [], closed: [],
            } }));
            return;
          }
          const receipt = terminalConnection && submission ? {
            requestId: 'secret', runId: run.id, executionId: 'exec-fixture', instanceId: 'bridge-fixture',
            submissionId: submission.submissionId, outcome: 'delivered', revision: 4,
          } : null;
          ws.send(JSON.stringify({ type: 'user_input_snapshot', payload: {
          runId: run.id, syncId: frame.payload.syncId, instanceId: 'bridge-fixture', revision: terminalConnection ? 5 : 0,
          requests: [], statuses: [], closed: receipt ? [{ ...receipt, status: 'closed', lifecycle: 'closed', revision: 5, receipt }] : [],
          receipts: receipt ? [receipt] : [],
          } }));
        }
      });
    });
    await page.goto(`${baseURL}/orchestrate/runs/${run.id}`, { waitUntil: 'domcontentloaded', timeout: 90000 });
    await page.waitForFunction(() => document.querySelectorAll('[class*="overflow-y-auto"]') && document.body.textContent.includes('Log entry 48'));
    assert.ok(socket, 'native run WebSocket must connect');
    for (const participant of ['Codex A', 'Codex B', 'Claude A', 'Claude B']) {
      assert.ok(await page.getByText(participant, { exact: true }).count() > 0, `timeline must identify ${participant}`);
    }
    // Codex async questions arrive as separate requests before any answer is sent.
    for (const id of ['async-1', 'async-2', 'async-3']) {
      socket.send(JSON.stringify({ type: 'user_input_request', payload: request(id, false) }));
    }
    for (const [index, id] of ['async-1', 'async-2', 'async-3'].entries()) {
      const asyncCard = page.locator(`#native-user-input-${id}`);
      await asyncCard.waitFor({ state: 'visible' });
      if (index === 1) await asyncCard.getByRole('textbox').fill('自定义异步答案');
      else await asyncCard.getByRole('radio', { name: 'B', exact: true }).check();
      await asyncCard.getByRole('button', { name: index < 2 ? '提交并继续下一题' : '提交回答', exact: true }).click();
      await waitForState(() => submission?.requestId === id);
      assert.equal(await asyncCard.isVisible(), true, 'do not advance before delivery is confirmed');
      assert.deepEqual(submission.answers[`q-${id}`], index === 1
        ? { optionIds: [], customText: '自定义异步答案' } : { optionIds: ['b'] });
      socket.send(JSON.stringify({ type: 'user_input_status', payload: {
        requestId: id, runId: run.id, executionId: 'exec-fixture', instanceId: 'bridge-fixture',
        submissionId: submission.submissionId, status: 'delivered', revision: 2,
      } }));
      if (index < 2) {
        await page.locator(`#native-user-input-async-${index + 2}`).waitFor({ state: 'visible' });
        await page.waitForFunction((next) => document.activeElement?.id === next, `native-user-input-async-${index + 2}`);
      } else {
        await page.locator('#native-user-input-panel').waitFor({ state: 'hidden' });
        const completed = page.getByRole('button', { name: /问答已完成.*3 题已送达.*查看记录/ });
        await completed.waitFor();
        if (process.env.USER_INPUT_SCREENSHOTS_DIR) await page.screenshot({ path: `${process.env.USER_INPUT_SCREENSHOTS_DIR}/completed-${width}.png`, fullPage: true });
        // The enhanced desktop timeline has a floating scroll control at the
        // row center. Click the unobstructed entry icon area, without forcing
        // the action or changing any delivery/recovery assertion.
        await completed.click({ position: { x: 16, y: 16 } });
        await asyncCard.getByRole('status').filter({ hasText: '答案已送达 CLI' }).waitFor();
      }
      const done = page.locator('#native-user-input-panel').getByRole('button', { name: new RegExp(`Codex A.*${index + 1}.*已送达`) });
      await done.waitFor();
      assert.equal(await done.locator('svg.lucide-check').count(), 1, 'delivered queue entries need a distinct completion icon and label');
    }
    await page.locator('#native-user-input-panel').getByRole('button', { name: 'Codex A · 1 · 1 题 · 已送达', exact: true }).click();
    await page.locator('#native-user-input-async-1').getByRole('status').filter({ hasText: '答案已送达 CLI' }).waitFor();
    assert.equal(await page.locator('#native-user-input-async-1 textarea').count(), 0, 'delivered answers are cleared, not shown as an empty unanswered form');
    for (const id of ['async-1', 'async-2', 'async-3']) socket.send(JSON.stringify({ type: 'user_input_status', payload: {
      requestId: id, runId: run.id, executionId: 'exec-fixture', instanceId: 'bridge-fixture', status: 'closed', revision: 3,
    } }));
    socket.send(JSON.stringify({ type: 'user_input_request', payload: request('choice', true) }));
    socket.send(JSON.stringify({ type: 'user_input_request', payload: request('secret', false, true) }));
    const inbox = page.getByRole('button', { name: /原生提问.*2.*等待回答.*1/ });
    await inbox.waitFor();
    const card = page.locator('#native-user-input-choice');
    const secretCard = page.locator('#native-user-input-secret');
    await card.waitFor();
    await card.getByRole('heading', { name: 'Codex A', exact: true }).waitFor();
    await secretCard.waitFor({ state: 'attached' });
    const panel = page.locator('#native-user-input-panel');
    await panel.waitFor({ state: 'visible' });
    assert.equal(await card.isVisible(), true, 'first question must open without clicking the inbox');
    assert.equal(await secretCard.isVisible(), false, 'only one editable request is shown');
    assert.ok(await panel.evaluate((element) => {
      const bounds = element.getBoundingClientRect();
      return bounds.top >= 0 && bounds.top < innerHeight && bounds.bottom <= innerHeight;
    }), 'question panel must remain visible above a long timeline');
    if (width < 768) await page.getByRole('button', { name: '配置 / 附件', exact: true }).click();
    await page.getByRole('status').filter({ hasText: '当前运行: Bridge 支持结构化提问' }).waitFor();
    if (width < 768) {
      await page.keyboard.press('Escape');
      await page.getByRole('dialog', { name: '运行配置', exact: true }).waitFor({ state: 'hidden' });
    }
    await card.getByRole('radio', { name: 'A', exact: true }).check();
    await panel.getByRole('button', { name: /Claude B.*2.*1 题/ }).click();
    await secretCard.getByRole('heading', { name: 'Claude B', exact: true }).waitFor();
    await secretCard.locator('input[type="password"]').fill('fixture-secret');
    await panel.getByRole('button', { name: /Codex.*1.*1 题/ }).click();
    assert.equal(await card.getByRole('radio', { name: 'A', exact: true }).isChecked(), true, 'queue switches must preserve the first draft');
    await panel.getByRole('button', { name: '收起' }).click();
    await panel.waitFor({ state: 'hidden' });
    const before = await inbox.boundingBox();
    assert.ok(before && before.y >= 0 && before.y + before.height <= 844, 'inbox must remain visible while events scroll');
    await inbox.focus();
    await page.keyboard.press('Enter');
    await panel.waitFor({ state: 'visible' });
    await page.waitForFunction(() => document.activeElement?.id === 'native-user-input-choice');
    assert.equal(await card.getByRole('radio', { name: 'A', exact: true }).isChecked(), true, 'collapse must preserve draft answers');
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'mobile page must not overflow');
    socket.send(JSON.stringify({ type: 'user_input_status', payload: {
      requestId: 'choice', runId: run.id, executionId: 'exec-fixture', instanceId: 'bridge-fixture',
      submissionId: 'other-tab', status: 'queued', revision: 2,
    } }));
    await card.getByRole('status').filter({ hasText: '正在送达' }).waitFor();
    assert.equal(await card.locator('fieldset').evaluate((element) => element.disabled), true, 'another tab reservation must disable this form');
    assert.equal(await card.getByRole('button', { name: '提交回答' }).count(), 0);
    await page.getByRole('button', { name: /原生提问.*2.*待确认.*1/ }).waitFor();
    socket.send(JSON.stringify({ type: 'user_input_status', payload: {
      requestId: 'choice', runId: run.id, executionId: 'exec-fixture', instanceId: 'bridge-fixture',
      submissionId: 'loser', status: 'rejected', source: 'hub', revision: 0,
    } }));
    assert.equal(await card.locator('fieldset').evaluate((element) => element.disabled), true, 'losing rejection cannot re-enable a queued form');
    socket.send(JSON.stringify({ type: 'user_input_status', payload: {
      requestId: 'choice', runId: run.id, executionId: 'exec-fixture', instanceId: 'bridge-fixture',
      status: 'closed', lifecycle: 'closed', revision: 3,
    } }));
    await page.waitForFunction(() => !document.getElementById('native-user-input-choice'));
    assert.equal(await page.getByRole('button', { name: /原生提问.*1/ }).count(), 1);
    await secretCard.waitFor({ state: 'visible' });
    assert.equal(await secretCard.locator('input[type="password"]').inputValue(), 'fixture-secret', 'closing another request must preserve this draft');
    await secretCard.getByRole('button', { name: '提交回答' }).click();
    await secretCard.getByRole('status').filter({ hasText: '正在送达' }).waitFor();
    assert.ok(submission, 'the answer must reach the mocked Hub before the ACK is lost');
    socket.send(JSON.stringify({ type: 'user_input_status', payload: {
      requestId: 'secret', runId: run.id, executionId: 'exec-fixture', instanceId: 'bridge-fixture',
      submissionId: submission.submissionId, status: 'delivered', revision: 4,
    } }));
    await panel.waitFor({ state: 'hidden' });
    await page.getByRole('button', { name: /问答已完成.*查看记录/ }).click({ position: { x: 16, y: 16 } });
    await secretCard.getByRole('status').filter({ hasText: '答案已送达 CLI' }).waitFor();
    socket.send(JSON.stringify({ type: 'user_input_request', payload: request('followup', false) }));
    const followupCard = page.locator('#native-user-input-followup');
    await followupCard.waitFor({ state: 'visible' });
    assert.equal(await secretCard.isVisible(), false, 'a new question must replace the selected delivered card');
    socket.send(JSON.stringify({ type: 'user_input_status', payload: {
      requestId: 'followup', runId: run.id, executionId: 'exec-fixture', instanceId: 'bridge-fixture',
      status: 'closed', lifecycle: 'closed', revision: 5,
    } }));
    await followupCard.waitFor({ state: 'detached' });
    // Exercise multi-question drafts and the actual serialized answer frame.
    const secretSubmission = submission;
    socket.send(JSON.stringify({ type: 'user_input_request', payload: {
      ...request('multi', true), cli: 'claude', workerId: 'claude-a', revision: 6, questions: [
        { id: 'approach', header: '方案', question: '选择实现方式', selectionMode: 'single', allowCustom: true,
          options: [{ id: 'native-id', label: '原生方案（推荐）', description: '沿用现有接口' }, { id: 'other-id', label: '替代方案' }] },
        { id: 'checks', question: '选择验证范围', selectionMode: 'multiple', allowCustom: true,
          options: [{ id: 'browser-id', label: '浏览器' }, { id: 'backend-id', label: '后端' }] },
        { id: 'notes', question: '补充说明', selectionMode: 'single', allowCustom: true },
      ],
    } }));
    const multiCard = page.locator('#native-user-input-multi');
    await multiCard.waitFor({ state: 'visible' });
    await multiCard.getByRole('heading', { name: 'Claude A', exact: true }).waitFor();
    assert.equal(await multiCard.getByRole('radio', { name: /原生方案/ }).isChecked(), false, 'recommendation is not a selection');
    await multiCard.getByRole('radio', { name: /原生方案/ }).check();
    await multiCard.getByRole('radio', { name: '自定义回答', exact: true }).check();
    assert.equal(await multiCard.getByRole('radio', { name: /原生方案/ }).isChecked(), false);
    await page.waitForFunction(() => document.activeElement?.tagName === 'TEXTAREA');
    await multiCard.getByRole('textbox').fill('我的方案');
    await multiCard.getByRole('radio', { name: /原生方案/ }).check();
    await multiCard.getByRole('button', { name: '下一题' }).click();
    await multiCard.getByRole('button', { name: '第 1 题，已填写', exact: true }).waitFor();
    assert.ok((await multiCard.getByRole('button', { name: '第 1 题，已填写', exact: true }).getAttribute('class')).includes('bg-emerald-100'));
    await multiCard.getByRole('checkbox', { name: '浏览器', exact: true }).check();
    await multiCard.getByRole('checkbox', { name: '后端', exact: true }).check();
    await multiCard.getByRole('checkbox', { name: '自定义回答', exact: true }).check();
    await multiCard.getByRole('textbox').fill('   ');
    assert.equal(await page.locator('button:has(svg.lucide-arrow-down-to-line)').count(), 0, 'timeline shortcut must not cover the open question panel');
    if (width < 768) {
      // Phone panels are mutually exclusive and keep the native-question draft mounted.
      await page.getByRole('button', { name: '运行对话', exact: true }).click();
      const conversation = page.getByRole('dialog', { name: '运行对话', exact: true });
      await conversation.waitFor({ state: 'visible' });
      await page.getByRole('region', { name: '运行对话', exact: true }).waitFor({ state: 'visible' });
      assert.equal(await page.getByRole('dialog', { name: '运行配置', exact: true }).isVisible(), false);
      await page.keyboard.press('Escape');
      await conversation.waitFor({ state: 'hidden' });
      await page.getByRole('button', { name: '配置 / 附件', exact: true }).click();
      const settings = page.getByRole('dialog', { name: '运行配置', exact: true });
      await settings.waitFor({ state: 'visible' });
      assert.equal(await conversation.isVisible(), false);
      await page.keyboard.press('Escape');
      await settings.waitFor({ state: 'hidden' });
      await multiCard.waitFor({ state: 'visible' });
      for (const name of ['浏览器', '后端', '自定义回答']) {
        assert.equal(await multiCard.getByRole('checkbox', { name, exact: true }).isChecked(), true, 'phone panel switches preserve question selections');
      }
      assert.equal(await multiCard.getByRole('textbox').inputValue(), '   ', 'phone panel switches preserve the unfinished custom answer');
    }
    if (process.env.USER_INPUT_SCREENSHOTS_DIR) {
      await page.screenshot({ path: `${process.env.USER_INPUT_SCREENSHOTS_DIR}/questions-${width}.png`, fullPage: true });
    }
    await multiCard.getByRole('button', { name: '下一题' }).click();
    await multiCard.getByRole('textbox').fill('保留上下文');
    assert.equal(await multiCard.getByRole('button', { name: '提交回答' }).isEnabled(), false, 'blank selected custom answer blocks completion');
    await multiCard.getByRole('button', { name: '上一题' }).click();
    assert.equal(await multiCard.getByRole('checkbox', { name: '浏览器', exact: true }).isChecked(), true);
    await multiCard.getByRole('textbox').fill('手机布局');
    await multiCard.getByRole('button', { name: /第 1 题/ }).click();
    assert.equal(await multiCard.getByRole('radio', { name: /原生方案/ }).isChecked(), true);
    assert.equal(await multiCard.getByRole('textbox').inputValue(), '我的方案', 'inactive custom draft survives navigation');
    await multiCard.getByRole('button', { name: /第 3 题/ }).click();
    await multiCard.getByRole('status').filter({ hasText: '已填写 3/3 题' }).waitFor();
    assert.equal(await multiCard.getByRole('textbox').inputValue(), '保留上下文');
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    await multiCard.getByRole('button', { name: '提交回答' }).click();
    await waitForState(() => submission?.requestId === 'multi');
    assert.deepEqual(submission.answers, {
      approach: { optionIds: ['native-id'] },
      checks: { optionIds: ['browser-id', 'backend-id'], customText: '手机布局' },
      notes: { optionIds: [], customText: '保留上下文' },
    });
    socket.send(JSON.stringify({ type: 'user_input_status', payload: {
      requestId: 'multi', runId: run.id, executionId: 'exec-fixture', instanceId: 'bridge-fixture',
      status: 'closed', lifecycle: 'closed', revision: 7,
    } }));
    await multiCard.waitFor({ state: 'detached' });
    submission = secretSubmission;
    const liveConnectionCount = connectionCount;
    runStatus = 'completed';
    socket.close();
    await page.getByRole('status').filter({ hasText: '答案已送达 CLI；原生问题已关闭。' }).waitFor();
    assert.ok(connectionCount > liveConnectionCount, 'disconnect followed by terminal run must open a receipt socket');
    await page.waitForTimeout(1500);
    assert.equal(terminalSocketClosed, true, 'receipt-only socket must close after its snapshot');
    assert.ok(terminalFrames.includes('user_input_sync') && terminalFrames.every((type) => type === 'user_input_sync' || type === 'heartbeat'), 'terminal connection may only query receipts and send heartbeats');
    assert.equal(connectionCount, liveConnectionCount + 1, 'completed receipt query must not reconnect itself');
    if (width === 1280) {
      runStatus = 'running';
      recoveryMode = 'load';
      await page.reload();
      const recoveryCard = page.locator('#native-user-input-refresh');
      await recoveryCard.waitFor();
      await recoveryCard.getByRole('radio', { name: 'A', exact: true }).check();
      await recoveryCard.getByRole('button', { name: '提交回答' }).click();
      await recoveryCard.getByRole('status').filter({ hasText: '正在送达' }).waitFor();
      assert.ok(heldSubmission);
      const stored = await page.evaluate(() => sessionStorage.getItem('codexBridge.nativeInputPending.v3:user-fixture'));
      const identities = JSON.parse(stored);
      assert.equal(identities.length, 1);
      assert.equal(identities[0].submissionId, heldSubmission.submissionId);
      assert.equal(Object.hasOwn(identities[0], 'answers'), false, 'session storage must never contain answer text');
      recoveryMode = 'query';
      await page.reload();
      await recoveryCard.waitFor();
      await page.waitForFunction(() => document.querySelector('#native-user-input-refresh fieldset')?.disabled);
      assert.equal(await recoveryCard.getByRole('radio', { name: 'A', exact: true }).isChecked(), false, 'refresh must not restore answer text');
      // The bootstrap snapshot cannot release the durable pending identity.
      await waitForState(() => fenceSync);
      assert.ok(fenceSync, 'refresh must query the exact persisted submission identity');
      assert.equal(fenceSync.pendingSubmissions[0].submissionId, heldSubmission.submissionId);
      const fencedReceipt = { requestId: 'refresh', runId: run.id, executionId: 'exec-fixture', instanceId: 'bridge-fixture',
        submissionId: heldSubmission.submissionId, outcome: 'not_sent', revision: 11 };
      socket.send(JSON.stringify({ type: 'user_input_snapshot', payload: {
        runId: run.id, syncId: fenceSync.syncId, instanceId: 'bridge-fixture', revision: 11,
        requests: [{ ...request('refresh', true), revision: 10 }], statuses: [], closed: [], receipts: [fencedReceipt],
        reconciliations: [{ ...fencedReceipt, status: 'rejected', lifecycle: 'open', source: 'bridge', receipt: fencedReceipt }],
      } }));
      await recoveryCard.getByRole('button', { name: '提交回答' }).waitFor();
      await recoveryCard.getByRole('radio', { name: 'B', exact: true }).check();
      await page.evaluate(() => {
        const original = Storage.prototype.setItem;
        window.restoreFixtureStorage = () => { Storage.prototype.setItem = original; };
        Storage.prototype.setItem = function (key, value) {
          if (key.startsWith('codexBridge.nativeInputPending.')) throw new Error('fixture quota exceeded');
          return original.call(this, key, value);
        };
      });
      await recoveryCard.getByRole('button', { name: '提交回答' }).click();
      await page.getByRole('status').filter({ hasText: '答案尚未发送' }).waitFor();
      assert.equal(recoveryAnswers.length, 1, 'storage failure must prevent network answer submission');
      await page.evaluate(() => window.restoreFixtureStorage());
      recoveryMode = 'hold';
      await recoveryCard.getByRole('button', { name: '提交回答' }).click();
      await recoveryCard.getByRole('status').filter({ hasText: '正在送达' }).waitFor();
      assert.equal(recoveryAnswers.length, 2, 'refresh/query must not automatically replay an answer');
      assert.notEqual(recoveryAnswers[1].submissionId, heldSubmission.submissionId, 'a fenced submission is replaced only with a new explicit ID');
    } else {
      featureEnabled = false;
      await page.reload();
      await page.getByRole('button', { name: '配置 / 附件', exact: true }).click();
      await page.getByRole('status').filter({ hasText: '当前运行: 原生结构化提问未启用。' }).waitFor();
      featureEnabled = true;
      hubProtocolVersion = 0;
      await page.reload();
      await page.getByRole('button', { name: '配置 / 附件', exact: true }).click();
      await page.getByRole('status').filter({ hasText: '当前运行: 当前 Hub 不支持原生问答 v3' }).waitFor();
    }
    assert.deepEqual(errors, []);
    await page.close();
  }
  console.log('Participant A/B identity, multi-question navigation, custom answers, queue drafts, delivery recovery, keyboard focus, mobile layout, and secret field passed.');
} finally {
  await browser.close();
}
