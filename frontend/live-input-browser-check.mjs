import assert from 'node:assert/strict';
import { chromium } from 'playwright';
const baseURL = process.env.LIVE_INPUT_TEST_URL || 'http://127.0.0.1:4201';
const browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
try {
  for (const width of [1280, 390]) {
    const page = await browser.newPage({ viewport: { width, height: 844 } });
    page.setDefaultTimeout(60000);
    const errors = []; page.on('pageerror', e => errors.push(e.message));
    const run = { id: 'live-fixture', agentId: 'agent-fixture', title: 'Dynamic input fixture', executionMode: 'dynamic', mode: 'collaboration', workerPair: 'codex-codex', prompt: 'Fixture', maxTurns: 0, status: 'running', createdAt: 1, updatedAt: 1 };
    let supported = true, ambiguous = false, reject = false, lastBody, continueBody;
    let statusSocket, getDelay = 0;
    let holdNextQuery = false, releaseHeldQuery, heldQueryReady;
    const messages = [], requests = [];
    await page.addInitScript(() => localStorage.setItem('codexBridge.language', 'zh'));
    await page.route('**/api/**', async route => {
      const url = new URL(route.request().url()); let data = {};
      if (url.pathname === '/api/me') data = { user: { id: 'user-fixture', username: 'fixture', createdAt: 1, features: [] } };
      else if (url.pathname === '/api/agents') data = { agents: [{ id: run.agentId, name: 'Fixture Bridge', machineId: 'fixture', hostname: 'fixture', lastSeenAt: 1, online: true, workingDirs: ['/tmp/fixture'], capabilities: { approvalPolicy: 'never', sandbox: 'danger-full-access', orchestrationLiveInput: supported ? 1 : 0, orchestration: { codex: { available: true }, claude: { available: true } } } }] };
      else if (url.pathname === '/api/orchestrations') data = { runs: [run] };
      else if (url.pathname.endsWith('/prompts')) {
        if (route.request().method() === 'POST') {
          lastBody = route.request().postDataJSON(); requests.push(lastBody);
          if (!lastBody.messageId) { continueBody = lastBody; run.status = 'running'; data = { run }; }
          else {
            if (reject) return route.fulfill({ status: 409, json: { message: 'Fixture native rejection' } });
            messages.push({ ...lastBody, id: lastBody.messageId, runId: run.id, status: 'queued', createdAt: 1, updatedAt: 1 });
            if (ambiguous) { ambiguous = false; return route.abort('failed'); }
            data = { message: messages.at(-1) };
          }
        } else {
          data = { messages: structuredClone(messages) };
          if (holdNextQuery) {
            holdNextQuery = false;
            await new Promise(resolve => { releaseHeldQuery = resolve; heldQueryReady(); });
          }
          if (getDelay) await new Promise(resolve => setTimeout(resolve, getDelay));
        }
      } else if (url.pathname.endsWith('/cancel')) { run.status = 'canceled'; data = { status: run.status }; }
      else if (url.pathname.endsWith('/events')) data = { events: [{ runId: run.id, seq: 1, kind: 'turn.delta', cli: 'codex', turnId: 'round-3', content: 'Current worker output', createdAt: 1 }], hasMoreBefore: false };
      else if (url.pathname === `/api/orchestrations/${run.id}`) data = { run };
      else if (url.pathname.includes('/presets')) data = { presets: [] };
      await route.fulfill({ json: data });
    });
    await page.routeWebSocket('**/ws/orchestrations?*', ws => { statusSocket = ws; ws.onMessage(() => {}); });
    const panel = page.getByRole('region', { name: '运行对话', exact: true });
    const openPhoneConversation = async () => {
      if (width < 768) await page.getByRole('button', { name: '运行对话', exact: true }).click();
    };
    await page.goto(`${baseURL}/orchestrate/runs/${run.id}`, { waitUntil: 'domcontentloaded' });
    await openPhoneConversation();
    await panel.waitFor();
    assert.equal(await page.locator('option').filter({ hasText: /^2 rounds$/ }).count(), 0);
    const input = panel.getByRole('textbox', { name: '追加指令' });
    const queue = panel.getByRole('button', { name: '排队发送', exact: true });
    const refresh = panel.getByRole('button', { name: '刷新送达状态' });
    if (process.env.LIVE_INPUT_SCREENSHOTS_DIR) {
      await page.screenshot({ path: `${process.env.LIVE_INPUT_SCREENSHOTS_DIR}/composer-dark-${width}.png`, fullPage: true });
      await page.evaluate(() => document.documentElement.classList.remove('dark'));
      await page.screenshot({ path: `${process.env.LIVE_INPUT_SCREENSHOTS_DIR}/composer-light-${width}.png`, fullPage: true });
    }
    await input.fill('First queued instruction'); await queue.click();
    await panel.getByText('排队中', { exact: true }).waitFor();
    assert.equal(lastBody.deliveryMode, 'queue'); assert.match(lastBody.messageId, /^[0-9a-f-]{36}$/);
    assert.equal(await input.inputValue(), '');
    // With responses slower than the old 3-second interval, automatic polling
    // must still apply results. No manual refresh or WebSocket receipt here.
    getDelay = 4500; messages[0].status = 'delivered';
    await panel.getByText('已送达 CLI', { exact: true }).waitFor({ timeout: 25000 });
    getDelay = 0;
    await page.reload({ waitUntil: 'domcontentloaded' });
    await openPhoneConversation();
    await panel.getByText('First queued instruction', { exact: true }).waitFor();
    await panel.getByText('已送达 CLI', { exact: true }).waitFor();
    await input.fill('Immediate direction'); await panel.getByRole('button', { name: '立即发送', exact: true }).click();
    await panel.getByText('Immediate direction', { exact: true }).waitFor(); assert.equal(lastBody.deliveryMode, 'immediate');
    // Pause every browser timer: only the real WebSocket receipt can refresh
    // this state. This prevents polling from accidentally passing the check.
    await page.clock.install();
    await page.clock.pauseAt(new Date(Date.now() + 10000));
    const queryHeld = new Promise(resolve => { heldQueryReady = resolve; });
    holdNextQuery = true;
    await refresh.click();
    await queryHeld;
    messages[1].status = 'failed'; messages[1].error = 'Native turn has ended';
    statusSocket.send(JSON.stringify({ type: 'orchestration_input_status', payload: { runId: run.id, messageId: messages[1].id, status: 'failed' } }));
    await panel.getByText('发送失败', { exact: true }).waitFor(); await panel.getByText('Native turn has ended').waitFor();
    const oldResponse = page.waitForResponse(response => response.url().endsWith('/prompts') && response.request().method() === 'GET');
    releaseHeldQuery();
    await (await oldResponse).finished();
    await page.waitForTimeout(100);
    assert.equal(await panel.getByText('发送失败', { exact: true }).count(), 1, 'late stale query cannot roll back a receipt');
    await page.clock.resume();
    ambiguous = true; await input.fill('Accepted despite lost HTTP response'); await queue.click();
    await panel.getByText('Accepted despite lost HTTP response', { exact: true }).waitFor();
    await page.waitForFunction(() => document.querySelector('textarea[placeholder="追加要求或纠正方向…"]')?.value === '');
    assert.equal(requests.length, 3, 'query recovery must not resend');
    const history = panel.getByRole('button', { name: '记录 3', exact: true });
    assert.equal(await history.getAttribute('aria-expanded'), 'false');
    assert.equal(await panel.getByText('First queued instruction', { exact: true }).count(), 0);
    await history.click();
    await panel.getByText('First queued instruction', { exact: true }).waitFor();
    await panel.getByText('Native turn has ended').waitFor();
    if (process.env.LIVE_INPUT_SCREENSHOTS_DIR) await page.screenshot({ path: `${process.env.LIVE_INPUT_SCREENSHOTS_DIR}/history-${width}.png`, fullPage: true });
    await history.click();
    reject = true; await input.fill('Rejected input stays editable'); await queue.click();
    await panel.getByRole('alert').waitFor(); const rejectedId = lastBody.messageId;
    assert.equal(await input.inputValue(), 'Rejected input stays editable'); await queue.click();
    await page.waitForFunction(() => !document.querySelector('textarea[placeholder="追加要求或纠正方向…"]')?.disabled);
    assert.equal(lastBody.messageId, rejectedId, 'retry keeps idempotency ID');
    reject = false; supported = false; await page.reload({ waitUntil: 'domcontentloaded' });
    await openPhoneConversation();
    await panel.getByText('此机器的 Bridge 尚不支持运行中追加指令，请更新本机 Bridge。').waitFor();
    await input.fill('Unsupported capability'); assert.equal(await queue.isDisabled(), true);
    supported = true; run.executionMode = 'bounded'; run.maxTurns = 2; run.status = 'completed';
    await page.reload({ waitUntil: 'domcontentloaded' });
    await openPhoneConversation();
    await panel.getByRole('button', { name: '继续运行', exact: true }).waitFor();
    assert.equal(await page.locator('option').filter({ hasText: /^2 rounds$/ }).count(), 1);
    await input.fill('Continue in the original run'); await panel.getByRole('button', { name: '继续运行', exact: true }).click();
    await queue.waitFor(); assert.equal(continueBody.prompt, 'Continue in the original run');
    assert.equal(continueBody.executionMode, undefined); assert.equal(continueBody.maxTurns, 2);
    await input.fill('Keep this draft when stopping');
    if (width < 768) {
      await page.keyboard.press('Escape');
      await page.getByRole('button', { name: '配置 / 附件', exact: true }).click();
    }
    await page.getByRole('button', { name: '停止运行', exact: true }).click();
    if (width < 768) { await page.keyboard.press('Escape'); await openPhoneConversation(); }
    await panel.getByRole('button', { name: '继续运行', exact: true }).waitFor();
    if (width < 768) {
      await page.keyboard.press('Escape');
      await page.getByRole('button', { name: '配置 / 附件', exact: true }).click();
    }
    await page.locator('aside').getByText('继续当前任务', { exact: true }).waitFor();
    await page.getByRole('button', { name: '前往输入', exact: true }).click();
    assert.equal(await input.evaluate(el => el === document.activeElement), true);
    assert.equal(await input.inputValue(), 'Keep this draft when stopping');
    assert.equal(await page.locator('textarea:visible').count(), 1, 'one composer after cancellation');
    const bounds = await input.boundingBox();
    assert.ok(bounds && bounds.y >= 0 && bounds.y + bounds.height <= 844, 'input is visible after navigation');
    await panel.getByRole('button', { name: '继续运行', exact: true }).click();
    await queue.waitFor();
    assert.equal(continueBody.prompt, 'Keep this draft when stopping');
    assert.equal(new URL(page.url()).pathname, `/orchestrate/runs/${run.id}`);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false);
    assert.deepEqual(errors, []);
    console.log(`live input browser ${width}px passed: slow automatic polling, WebSocket receipt with polling paused, queue, immediate, durable reload, errors, idempotency, capability, continuity`);
    await page.close();
  }
} finally { await browser.close(); }
