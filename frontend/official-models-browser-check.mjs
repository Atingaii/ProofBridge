import assert from 'node:assert/strict';
import { chromium } from 'playwright';

// Run against VITE_LEADING=true VITE_OFFICIAL_CODEX_MODELS=true. All API and WS
// traffic is mocked: these checks never reset a real endpoint or run a model.
const baseURL = process.env.OFFICIAL_MODELS_TEST_URL || 'http://127.0.0.1:4203';
const waitUntil = async (predicate, message = 'fixture did not converge') => {
  const deadline = Date.now() + 15000;
  while (!await predicate()) {
    assert.ok(Date.now() < deadline, message);
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
};
const gate = () => { let release; const promise = new Promise((resolve) => { release = resolve; }); return { promise, release }; };
const models = [
  { model: 'native-reasoner', displayName: 'Native reasoner', supportedReasoningEfforts: ['low', 'high'], defaultReasoningEffort: 'high', isDefault: true },
  { model: 'native-fast', displayName: 'Native fast', supportedReasoningEfforts: ['minimal', 'low'], defaultReasoningEffort: 'low' },
  { model: 'native-plain', displayName: 'Native plain', supportedReasoningEfforts: [] },
];
const provider = { id: 'provider-codex', cli: 'codex', name: 'Provider fixture', model: 'provider-only', reasoningLevels: ['low', 'high'], reasoningDefault: 'low', reasoningEffort: 'low' };
const browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 980 } });
  page.setDefaultTimeout(45000);
  const errors = [];
  page.on('pageerror', (err) => errors.push(err.message));
  let featureEnabled = true, bridgeVersion = 3, catalogFailure = false, slowCatalog;
  const requests = [], catalogs = [], resets = [], runs = [];
  const agents = () => ['a', 'b', 'old'].map((id) => ({ id: `agent-${id}`, name: `Machine ${id}`, hostname: id, machineId: id, online: true, lastSeenAt: 1, workingDirs: ['/tmp/official-models-fixture'],
    capabilities: { approvalPolicy: 'never', sandbox: 'danger-full-access', orchestrationLiveInput: 1,
      orchestration: { codex: { available: true }, claude: { available: true } },
      configSwitcher: { version: id === 'old' ? 2 : bridgeVersion, clis: ['codex', 'claude'], keyId: 'fixture', publicKey: 'fixture' } } }));
  await page.addInitScript(() => { localStorage.setItem('codexBridge.language', 'zh'); localStorage.setItem('codexBridge.selectedAgentId', 'agent-a'); });
  await page.route('**/api/**', async (route) => {
    const request = route.request(), url = new URL(request.url()), path = url.pathname;
    let data = {}, heldDirectory;
    if (path === '/api/me') data = { user: { id: 'official-fixture', username: 'fixture', createdAt: 1, features: featureEnabled ? ['official-codex-worker-models'] : [] } };
    else if (path === '/api/agents') data = { agents: agents() };
    else if (path.endsWith('/official-models')) {
      catalogs.push(path);
      assert.equal(url.searchParams.get('cli'), 'codex');
      if (slowCatalog) { heldDirectory = slowCatalog; slowCatalog = undefined; await heldDirectory.promise; }
      if (catalogFailure) return route.fulfill({ status: 502, json: { message: 'Native directory unavailable' } }).catch(() => {});
      data = { models: path.includes('agent-b') ? [{ model: 'machine-b-model', displayName: 'Machine B native', supportedReasoningEfforts: ['medium'] }] : models };
    } else if (path.endsWith('/presets')) data = { presets: [provider] };
    else if (path.endsWith('/official-reset')) { assert.equal(request.method(), 'POST'); resets.push(request.postDataJSON()); }
    else if (path === '/api/orchestrations' && request.method() === 'POST') {
      const body = request.postDataJSON(); requests.push({ path, body });
      const run = { ...body, id: `official-run-${runs.length + 1}`, title: 'Official fixture run', status: 'completed', createdAt: 1, updatedAt: 1 };
      runs.push(run); data = { run };
    } else if (path === '/api/orchestrations') data = { runs };
    else if (path.endsWith('/prompts') && request.method() === 'POST') {
      const body = request.postDataJSON(); requests.push({ path, body }); data = { run: runs.find((run) => path.includes(run.id)) };
    } else if (path.endsWith('/events')) data = { events: [], hasMoreBefore: false, hasMoreAfter: false };
    else if (path.endsWith('/prompts')) data = { messages: [] };
    else if (path.startsWith('/api/orchestrations/')) data = { run: runs.find((run) => path.endsWith(run.id)) };
    await route.fulfill({ json: data }).catch(() => {}); // Catalog fetches are canceled on target switches.
    if (heldDirectory) heldDirectory.done = true;
  });
  await page.routeWebSocket('**/ws/**', (ws) => ws.onMessage(() => {}));
  const model = (slot) => page.getByLabel(`${slot} 模型`, { exact: true });
  const effort = (slot) => page.getByLabel(`${slot} 思考强度`, { exact: true });
  const preset = (slot) => page.getByLabel(`${slot} 工作器模型预设`, { exact: true });
  const endpoint = () => page.getByLabel('选择 CLI 端').filter({ visible: true }).first();
  const input = () => page.locator('textarea:visible').first();
  const pair = async (label) => {
    const button = page.getByRole('button', { name: label, exact: true }).first();
    if (await button.getAttribute('aria-pressed') !== 'true') await button.click();
  };
  const confirm = async () => {
    for (const select of await page.locator('select[aria-label$="工作器模型预设"]').all()) {
      if (await select.inputValue() === '__official__') {
        const button = select.locator('..').getByRole('button', { name: '确认', exact: true });
        if (await button.count()) { await button.click(); await waitUntil(async () => !await button.count()); }
      }
    }
  };
  const start = async (text) => {
    await confirm(); await input().fill(text);
    const count = requests.length;
    await page.getByRole('button', { name: '开始', exact: true }).click();
    await waitUntil(() => requests.length === count + 1);
    await waitUntil(() => new URL(page.url()).pathname.includes('/runs/'));
    return requests.at(-1).body;
  };
  await page.goto(`${baseURL}/orchestrate`, { waitUntil: 'domcontentloaded' });
  await pair('Codex + Codex');
  await waitUntil(async () => !await model('Codex A').isDisabled());
  assert.equal(await model('Codex A').inputValue(), '', 'native default is not turned into an explicit model override');
  assert.equal(await effort('Codex A').isDisabled(), true);
  assert.equal(await model('Codex A').locator('option[value="provider-only"]').count(), 0, 'provider catalog is separate');
  assert.equal(resets.length, 0, 'directory read must not confirm/reset login');
  await model('Codex A').selectOption('native-reasoner'); await effort('Codex A').selectOption('high');
  await model('Codex B').selectOption('native-reasoner'); await effort('Codex B').selectOption('low');
  await model('Codex A').selectOption('native-fast');
  assert.equal(await effort('Codex A').inputValue(), '', 'unsupported previous effort is cleared');
  assert.equal(await effort('Codex A').locator('option[value="high"]').count(), 0);
  assert.equal(await effort('Codex B').inputValue(), 'low', 'A change leaves B untouched');
  await model('Codex A').selectOption('native-plain'); assert.equal(await effort('Codex A').isDisabled(), true);
  await model('Codex A').selectOption('native-fast'); await effort('Codex A').selectOption('minimal');
  const initial = await start('Independent official workers');
  assert.deepEqual(initial.workerProfileModels, { 'codex-a': 'native-fast', 'codex-b': 'native-reasoner' });
  assert.deepEqual(initial.workerProfileEfforts, { 'codex-a': 'minimal', 'codex-b': 'low' });
  assert.deepEqual(initial.workerProfilePresetIds, { 'codex-a': '__official__', 'codex-b': '__official__' });
  const runID = runs.at(-1).id;

  // Fresh page has untouched selectors: continuation must reuse backend bindings.
  await page.reload({ waitUntil: 'domcontentloaded' });
  const continueInput = page.getByRole('region', { name: '运行对话', exact: true }).getByRole('textbox', { name: '追加指令' });
  await continueInput.fill('Continue original threads');
  await page.getByRole('button', { name: '继续运行', exact: true }).click();
  await waitUntil(() => requests.length === 2);
  assert.equal(requests.at(-1).path, `/api/orchestrations/${runID}/prompts`);
  for (const field of ['workerProfileModels', 'workerProfilePresetIds', 'workerProfileEfforts']) assert.equal(field in requests.at(-1).body, false, `${field} omitted for untouched follow-up`);

  await continueInput.fill('Explicit replacement uses existing flow');
  await waitUntil(async () => !await page.getByRole('button', { name: '继续运行', exact: true }).isDisabled());
  await model('Codex A').selectOption('native-reasoner'); await effort('Codex A').selectOption('high');
  await page.getByRole('button', { name: '继续运行', exact: true }).click();
  await waitUntil(() => requests.length === 3);
  assert.deepEqual(requests.at(-1).body.workerProfileModels, { 'codex-a': 'native-reasoner' });
  assert.deepEqual(requests.at(-1).body.workerProfileEfforts, { 'codex-a': 'high' });

  await page.goto(`${baseURL}/orchestrate`, { waitUntil: 'domcontentloaded' });
  await page.getByRole('button', { name: '新运行', exact: true }).click(); await pair('Codex + Codex');
  await waitUntil(async () => !await model('Codex A').isDisabled());
  const defaults = await start('Keep CLI defaults');
  assert.equal('workerProfileModels' in defaults, false);
  assert.deepEqual(defaults.workerProfileEfforts, {});

  await page.getByRole('button', { name: '新运行', exact: true }).click();
  await preset('Codex A').selectOption('provider-codex'); await preset('Codex B').selectOption('provider-codex');
  assert.equal(await model('Codex A').count(), 0);
  await effort('Codex B').selectOption('high');
  const thirdParty = await start('Provider path unchanged');
  assert.equal('workerProfileModels' in thirdParty, false);
  assert.deepEqual(thirdParty.workerProfileEfforts, { 'codex-a': 'low', 'codex-b': 'high' });

  await page.getByRole('button', { name: '新运行', exact: true }).click();
  await endpoint().selectOption('agent-old'); await pair('Codex + Codex');
  await waitUntil(async () => await model('Codex A').count() === 1 && await model('Codex A').isDisabled());
  await page.getByText('请更新此机器的 Bridge，以选择官方模型和思考强度。').first().waitFor();
  assert.equal(catalogs.some((path) => path.includes('agent-old')), false, 'old Bridge is not queried');

  // A late native directory from machine A cannot populate machine B controls.
  slowCatalog = gate(); const held = slowCatalog;
  const before = catalogs.length;
  await endpoint().selectOption('agent-a');
  await waitUntil(() => catalogs.length > before);
  await endpoint().selectOption('agent-b');
  await waitUntil(async () => await model('Codex A').locator('option[value="machine-b-model"]').count() === 1);
  held.release();
  await waitUntil(() => held.done, 'late directory response settled');
  await model('Codex A').selectOption('machine-b-model');
  assert.equal(await model('Codex A').locator('option[value="native-reasoner"]').count(), 0);
  await effort('Codex A').selectOption('medium');
  await pair('Claude + Claude');
  assert.equal(await model('Codex A').count(), 0);
  assert.equal(await effort('Claude A').isDisabled(), true);
  await pair('Codex + Codex');
  await waitUntil(async () => !await model('Codex A').isDisabled());
  assert.equal(await model('Codex A').inputValue(), '', 'pair switch clears model');
  assert.equal(await effort('Codex A').inputValue(), '', 'pair switch clears effort');

  await pair('Claude + Codex');
  await waitUntil(async () => !await model('Codex').isDisabled());
  await model('Codex').selectOption('machine-b-model'); await effort('Codex').selectOption('medium');
  assert.equal(await effort('Claude').isDisabled(), true, 'Claude official path remains unchanged');
  const mixed = await start('Existing mixed pair slot semantics');
  assert.deepEqual(mixed.workerProfileModels, { codex: 'machine-b-model' });
  assert.deepEqual(mixed.workerProfileEfforts, { codex: 'medium' });
  await page.getByRole('button', { name: '新运行', exact: true }).click(); await pair('Codex + Codex');

  catalogFailure = true;
  await endpoint().selectOption('agent-a');
  await page.getByText(/官方模型目录暂不可用。 Native directory unavailable/).first().waitFor();
  assert.equal(await model('Codex A').isDisabled(), true);
  catalogFailure = false; featureEnabled = false;
  const beforeDenied = catalogs.length;
  await page.reload({ waitUntil: 'domcontentloaded' }); await pair('Codex + Codex');
  await preset('Codex A').waitFor();
  assert.equal(await model('Codex A').count(), 0, 'feature-denied user retains existing controls');
  assert.equal(await effort('Codex A').isDisabled(), true);
  assert.equal(catalogs.length, beforeDenied, 'feature-denied user does not query directory');
  assert.deepEqual(errors, []);
  await page.close();
  console.log('Official model browser checks passed: independent slots, catalog isolation, defaults, provider/Claude compatibility, continuation, old Bridge and stale responses.');
} finally { await browser.close(); }
