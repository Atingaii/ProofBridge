import assert from 'node:assert/strict';
import { chromium } from 'playwright';

// Isolated UI/API boundary fixtures. This is not a real Hub/model/SMTP acceptance run.
const baseURL = process.env.AUTOMATION_UI_TEST_URL || 'http://127.0.0.1:37648';
const browser = await chromium.launch({
  headless: true,
  ...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } : {}),
  args: ['--no-sandbox', '--disable-dev-shm-usage'],
});
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
page.setDefaultTimeout(15000);
const errors = [];
page.on('pageerror', (err) => errors.push(err.message));
page.on('dialog', (dialog) => dialog.accept());

let token = 'pb_mcp_ui-fixture-first', mailReady = true, loadFailure = false, conflict = false;
let version = 1;
let preferences = { email: 'owner@example.test', completionEnabled: true, attentionEnabled: true, defaultEndpointId: '', monitoringMode: 'interactive' };
const writes = [];
const access = () => ({
  token, mcpUrl: 'https://lab.example.test/mcp',
  codexConfig: token ? '[mcp_servers.proofbridge]\nurl = "https://lab.example.test/mcp"\nhttp_headers = { Authorization = "Bearer ' + token + '" }\n' : '',
  claudeConfig: token ? JSON.stringify({ mcpServers: { proofbridge: { type: 'http', url: 'https://lab.example.test/mcp', headers: { Authorization: 'Bearer ' + token } } } }, null, 2) : '',
  skillInstallCommands: { codex: 'sh install.sh --client codex --hub https://lab.example.test', claude: 'sh install.sh --client claude --hub https://lab.example.test' },
  mailReady,
});
await page.addInitScript(() => {
  localStorage.setItem('codexBridge.language', 'zh');
  window.__automationCopied = '';
  window.__automationDenyCopy = false;
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: {
    writeText: async (value) => {
      if (window.__automationDenyCopy) throw new Error('Clipboard denied');
      window.__automationCopied = value;
    },
  } });
  document.execCommand = () => false;
});
await page.route('**/api/**', async (route) => {
  const path = new URL(route.request().url()).pathname;
  const method = route.request().method();
  if (path === '/api/me') return route.fulfill({ json: { user: { id: 'automation-owner', username: 'automation-owner', createdAt: 1, features: [] } } });
  if (path === '/api/agents') return route.fulfill({ json: { agents: [{ id: 'owned-endpoint', name: 'My workstation', machineId: 'own', hostname: 'own', online: true, lastSeenAt: 1, workingDirs: ['/tmp/project'] }] } });
  if (path === '/api/automation/access') {
    return route.fulfill(loadFailure ? { status: 503, json: { message: 'Connection settings temporarily unavailable' } } : { json: access() });
  }
  if (path === '/api/automation/access/rotate' || path === '/api/automation/access/revoke') {
    assert.equal(method, 'POST');
    writes.push({ path });
    token = path.endsWith('/revoke') ? null : 'pb_mcp_ui-fixture-rotated';
    return route.fulfill({ json: access() });
  }
  if (path === '/api/automation/preferences') {
    if (method === 'PUT') {
      const body = route.request().postDataJSON();
      writes.push({ path, body });
      if (conflict) return route.fulfill({ status: 409, json: { message: 'Version conflict' } });
      assert.equal(body.scope, 'account');
      assert.equal(body.expectedVersion, version);
      preferences = body.data; version++;
    }
    return route.fulfill({ json: { scope: 'account', version, data: preferences } });
  }
  if (path.endsWith('/sessions')) return route.fulfill({ json: { sessions: [] } });
  if (path.endsWith('/presets')) return route.fulfill({ json: { presets: [] } });
  return route.fulfill({ json: { runs: [], events: [], agents: [] } });
});
try {
  await page.goto(`${baseURL}/orchestrate`, { waitUntil: 'domcontentloaded' });
  await page.getByRole('button', { name: '设置', exact: true }).click();
  const card = page.getByTestId('automation-settings');
  await card.getByRole('button', { name: '终端与邮件', exact: true }).click();
  await card.getByLabel('个人 MCP Token', { exact: true }).waitFor();

  assert.equal(await card.getByLabel('个人 MCP Token', { exact: true }).getAttribute('type'), 'password');
  assert.ok(!(await card.getByTestId('automation-config-preview').textContent()).includes(token), 'configuration preview leaked a hidden token');
  await card.getByRole('button', { name: '复制 MCP 配置', exact: true }).click();
  assert.ok((await page.evaluate(() => window.__automationCopied)).includes(token), 'copy must include complete working credentials');
  await card.getByLabel('终端客户端').selectOption('claude');
  await card.getByRole('button', { name: '复制 MCP 配置', exact: true }).click();
  const claude = JSON.parse(await page.evaluate(() => window.__automationCopied));
  assert.equal(claude.mcpServers.proofbridge.type, 'http');
  assert.equal(claude.mcpServers.proofbridge.headers.Authorization, 'Bearer ' + token);
  await card.getByRole('button', { name: '复制 Skill 安装命令', exact: true }).click();
  assert.match(await page.evaluate(() => window.__automationCopied), /--client claude/);

  await card.getByLabel('收件邮箱', { exact: true }).fill('updated@example.test');
  await card.getByLabel('默认执行端点', { exact: true }).selectOption('owned-endpoint');
  await card.getByRole('button', { name: '保存设置', exact: true }).click();
  await card.getByText('设置已保存。两个终端客户端将共用这些默认值。', { exact: true }).waitFor();
  assert.equal(preferences.email, 'updated@example.test');
  assert.equal(preferences.defaultEndpointId, 'owned-endpoint');

  // A version conflict preserves the user's unsaved input.
  conflict = true;
  await card.getByLabel('收件邮箱', { exact: true }).fill('unsaved@example.test');
  await card.getByRole('button', { name: '保存设置', exact: true }).click();
  await card.getByRole('alert').getByText('设置已在其他客户端更新。请重新加载后再保存。', { exact: true }).waitFor();
  assert.equal(await card.getByLabel('收件邮箱', { exact: true }).inputValue(), 'unsaved@example.test');
  conflict = false;

  // Clipboard denial must not announce success.
  await page.evaluate(() => { window.__automationDenyCopy = true; });
  await card.getByRole('button', { name: '复制 Token', exact: true }).click();
  await card.getByRole('alert').getByText('复制失败，请选中内容后手动复制。', { exact: true }).waitFor();
  await page.evaluate(() => { window.__automationDenyCopy = false; });

  await card.getByRole('button', { name: '重新生成 Token', exact: true }).click();
  await card.getByLabel('个人 MCP Token', { exact: true }).waitFor();
  await card.getByRole('button', { name: '复制 Token', exact: true }).click();
  assert.equal(await page.evaluate(() => window.__automationCopied), 'pb_mcp_ui-fixture-rotated');
  await card.getByRole('button', { name: '撤销 Token', exact: true }).click();
  await card.getByText('Token 已撤销。需要再次接入时，重新生成并复制配置。').first().waitFor();
  assert.equal(await card.getByRole('button', { name: '复制 Token', exact: true }).isDisabled(), true);
  assert.equal(await card.getByLabel('个人 MCP Token', { exact: true }).inputValue(), '');

  // Reload a temporarily unavailable setup without silently creating a new token.
  loadFailure = true;
  await card.getByRole('button', { name: '终端与邮件', exact: true }).click();
  await card.getByRole('button', { name: '终端与邮件', exact: true }).click();
  await card.getByRole('alert').getByText('Connection settings temporarily unavailable', { exact: true }).waitFor();
  loadFailure = false; mailReady = false;
  await card.getByRole('button', { name: '重新加载', exact: true }).click();
  await card.getByText(/管理员尚未配置可用邮件服务/).waitFor();
  await card.getByLabel('默认结果接收方式', { exact: true }).selectOption('background-mail');
  const writesBefore = writes.length;
  await card.getByRole('button', { name: '保存设置', exact: true }).click();
  await card.getByRole('alert').getByText(/后台邮件模式需要可用邮件服务/).waitFor();
  assert.equal(writes.length, writesBefore, 'unavailable background notification must not be silently accepted');
  await card.getByLabel('默认结果接收方式', { exact: true }).selectOption('interactive');

  await page.setViewportSize({ width: 390, height: 844 });
  const heading = card.getByRole('button', { name: '终端与邮件', exact: true });
  await heading.scrollIntoViewIfNeeded();
  const box = await heading.boundingBox();
  assert.ok(box.height >= 48, 'phone setup controls need usable touch targets');
  const overflow = await card.evaluate((element) => element.scrollWidth > element.clientWidth + 1);
  assert.equal(overflow, false, 'MCP configuration should wrap without horizontal card overflow');
  const secrets = await page.evaluate(() => Object.values(localStorage).some((value) => value.includes('pb_mcp_ui-fixture')));
  assert.equal(secrets, false, 'PAT must not be cached in browser storage');
  assert.deepEqual(errors, []);
  console.log('Automation UI passed: complete two-client copy, hidden credentials, installer selection, preference/version behavior, clipboard failures, rotate/revoke, unavailable SMTP and mobile layout.');
} finally {
  await browser.close();
}
