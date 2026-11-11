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

let mcpURL = 'https://lab.example.test/mcp';
let token = 'pb_mcp_ui-fixture-first', mailReady = true, loadFailure = false, conflict = false;
let mailTestFailure = false;
let mailDeliveryMode, testDeliveryMode;
let version = 1;
let preferences = { email: 'owner@example.test', completionEnabled: true, attentionEnabled: true, defaultEndpointId: '', monitoringMode: 'interactive' };
const writes = [];
const quoteShell = (value) => "'" + value.replaceAll("'", "'\"'\"'") + "'";
const githubInstaller = (client) => 'sh -c ' + quoteShell(`set -eu; umask 077; installer=$(mktemp "\${TMPDIR:-/tmp}/proofbridge-install.XXXXXXXX"); trap 'rm -f "$installer"' EXIT HUP INT TERM; curl --fail --silent --show-error --location --connect-timeout 15 --max-time 90 --retry 2 --proto '=https' --proto-redir '=https' 'https://raw.githubusercontent.com/Atingaii/ProofBridge/proofbridge-skill-v1.1.0/internal/automationassets/install.sh' --output "$installer"; sh "$installer" --client ${client} --scope user`);
const access = () => ({
  token, mcpUrl: mcpURL,
  codexConfig: token ? '[mcp_servers.proofbridge]\nurl = "' + mcpURL + '"\nhttp_headers = { Authorization = "Bearer ' + token + '" }\n' : '',
  claudeConfig: token ? JSON.stringify({ mcpServers: { proofbridge: { type: 'http', url: mcpURL, headers: { Authorization: 'Bearer ' + token } } } }, null, 2) : '',
  skillInstallCommands: { codex: githubInstaller('codex'), claude: githubInstaller('claude') },
  mailReady, mailDeliveryMode,
});
await page.addInitScript(() => {
  if (!localStorage.getItem('codexBridge.language')) localStorage.setItem('codexBridge.language', 'zh');
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
  if (path === '/api/automation/mail/test') {
    assert.equal(method, 'POST');
    assert.deepEqual(route.request().postDataJSON(), {});
    writes.push({ path });
    if (mailTestFailure) return route.fulfill({ status: 502, json: { message: 'Mail test rejected; check SMTP settings.' } });
    return route.fulfill({ json: { smtpAccepted: true, status: 'accepted', deliveryMode: testDeliveryMode } });
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
  await card.getByTestId('automation-config-preview').waitFor();
  assert.equal(await card.getByLabel('个人 MCP Token', { exact: true }).count(), 0);
  assert.equal(await card.getByLabel('MCP 地址', { exact: true }).count(), 0);
  for (const removed of ['一次接入，在 Codex 或 Claude Code', '复制的配置包含个人凭据', '在终端运行安装命令', '项目仍在选中的用户机器', '可在终端用自然语言保存模型组合']) {
    assert.equal(await card.getByText(removed, { exact: false }).count(), 0, 'removed setup explanation must not remain');
  }
  assert.equal(await card.getByTestId('automation-config-preview').textContent(), access().codexConfig);
  const prompt = card.getByTestId('automation-setup-prompt');
  assert.ok((await prompt.textContent()).includes(access().codexConfig.trim()));
  assert.ok((await prompt.textContent()).includes(access().skillInstallCommands.codex));
  assert.match(await prompt.textContent(), /GitHub/);
  assert.match(await prompt.textContent(), /proofbridge-skill-v1\.1\.0/);
  assert.match(await prompt.textContent(), /proofbridge_get_context/);
  assert.match(await prompt.textContent(), /proofbridge_list_endpoints/);
  await card.getByRole('button', { name: '复制接入提示词', exact: true }).click();
  assert.equal(await page.evaluate(() => window.__automationCopied), await prompt.textContent());
  await card.getByRole('button', { name: '复制 MCP 配置', exact: true }).click();
  assert.ok((await page.evaluate(() => window.__automationCopied)).includes(token), 'copy must include complete working credentials');
  await card.getByLabel('终端客户端').selectOption('claude');
  await card.getByRole('button', { name: '复制 MCP 配置', exact: true }).click();
  const claude = JSON.parse(await page.evaluate(() => window.__automationCopied));
  assert.equal(claude.mcpServers.proofbridge.type, 'http');
  assert.equal(claude.mcpServers.proofbridge.headers.Authorization, 'Bearer ' + token);
  await card.getByRole('button', { name: '复制接入提示词', exact: true }).click();
  assert.match(await page.evaluate(() => window.__automationCopied), /--client claude/);
  assert.ok((await prompt.textContent()).includes(access().claudeConfig.trim()));
  assert.ok(!(await prompt.textContent()).includes('--client codex'));

  await card.getByLabel('收件邮箱', { exact: true }).fill('updated@example.test');
  await card.getByLabel('默认执行端点', { exact: true }).selectOption('owned-endpoint');
  await card.getByRole('button', { name: '保存设置', exact: true }).click();
  await card.getByText('设置已保存。两个终端客户端将共用这些默认值。', { exact: true }).waitFor();
  assert.equal(preferences.email, 'updated@example.test');
  assert.equal(preferences.defaultEndpointId, 'owned-endpoint');
  const testMail = card.getByRole('button', { name: '发送测试邮件', exact: true });
  assert.equal(await testMail.isDisabled(), false);
  await testMail.click();
  await card.getByText('邮件服务已接受测试邮件。请检查收件箱与垃圾邮件；本地测试服务请查看其收件箱。', { exact: true }).waitFor();
  mailTestFailure = true;
  await testMail.click();
  await card.getByRole('alert').getByText('Mail test rejected; check SMTP settings.', { exact: true }).waitFor();
  mailTestFailure = false;

  // New preview responses must not imply delivery to a real mailbox.
  mailDeliveryMode = 'preview'; testDeliveryMode = 'preview';
  await card.getByRole('button', { name: '重新加载', exact: true }).click();
  await card.getByText('仅本地邮件预览，真实邮箱不会收到。', { exact: true }).waitFor();
  assert.equal(await card.getByLabel('默认结果接收方式', { exact: true }).locator('option[value="background-mail"]').textContent(), '提交后投递到本地预览');
  await testMail.click();
  await card.getByText('测试邮件已进入本地预览收件箱，未向真实邮箱投递。', { exact: true }).waitFor();
  // A missing test field can fall back to the access mode; an explicit result wins.
  testDeliveryMode = undefined;
  await testMail.click();
  await card.getByText('测试邮件已进入本地预览收件箱，未向真实邮箱投递。', { exact: true }).waitFor();
  testDeliveryMode = 'smtp';
  await testMail.click();
  await card.getByText('邮件服务已接受测试邮件。请检查收件箱与垃圾邮件；本地测试服务请查看其收件箱。', { exact: true }).waitFor();
  mailDeliveryMode = undefined; testDeliveryMode = undefined;


  // A version conflict preserves the user's unsaved input.
  conflict = true;
  await card.getByLabel('收件邮箱', { exact: true }).fill('unsaved@example.test');
  assert.equal(await testMail.isDisabled(), true, 'test mail must use a saved email');
  await card.getByRole('button', { name: '保存设置', exact: true }).click();
  await card.getByRole('alert').getByText('设置已在其他客户端更新。请重新加载后再保存。', { exact: true }).waitFor();
  assert.equal(await card.getByLabel('收件邮箱', { exact: true }).inputValue(), 'unsaved@example.test');
  conflict = false;

  // Clipboard denial must not announce success.
  await page.evaluate(() => { window.__automationDenyCopy = true; });
  await card.getByRole('button', { name: '复制接入提示词', exact: true }).click();
  await card.getByRole('alert').getByText('复制失败，请选中内容后手动复制。', { exact: true }).waitFor();
  await page.evaluate(() => { window.__automationDenyCopy = false; });

  await card.getByRole('button', { name: '重新生成 Token', exact: true }).click();
  await card.getByTestId('automation-config-preview').getByText('pb_mcp_ui-fixture-rotated', { exact: false }).waitFor();
  assert.ok(!(await prompt.textContent()).includes('pb_mcp_ui-fixture-first'));
  await card.getByRole('button', { name: '复制接入提示词', exact: true }).click();
  assert.ok((await page.evaluate(() => window.__automationCopied)).includes('pb_mcp_ui-fixture-rotated'));
  await card.getByRole('button', { name: '撤销 Token', exact: true }).click();
  await card.getByText('Token 已撤销。需要再次接入时，重新生成并复制配置。').first().waitFor();
  assert.equal(await prompt.count(), 0);
  assert.equal(await card.getByTestId('automation-config-preview').count(), 0);
  assert.equal(await card.getByRole('button', { name: '复制接入提示词', exact: true }).count(), 0);
  assert.ok(!(await card.textContent()).includes('pb_mcp_ui-fixture'));

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

  // Keep the complete configuration and prompt present for narrow-screen checks.
  await card.getByRole('button', { name: '重新生成 Token', exact: true }).click();
  await prompt.waitFor();
  await page.setViewportSize({ width: 390, height: 844 });
  const heading = card.getByRole('button', { name: '终端与邮件', exact: true });
  await heading.scrollIntoViewIfNeeded();
  const box = await heading.boundingBox();
  assert.ok(box.height >= 48, 'phone setup controls need usable touch targets');
  const overflow = await card.evaluate((element) => element.scrollWidth > element.clientWidth + 1);
  assert.equal(overflow, false, 'MCP configuration should wrap without horizontal card overflow');
  const copyBox = await card.getByRole('button', { name: '复制接入提示词', exact: true }).boundingBox();
  assert.ok(copyBox.height >= 48, 'phone prompt copy needs a usable touch target');
  if (process.env.AUTOMATION_UI_SCREENSHOT_PATH) await page.screenshot({ path: process.env.AUTOMATION_UI_SCREENSHOT_PATH });

  await page.getByRole('button', { name: 'English', exact: true }).click();
  await prompt.waitFor();
  await card.getByLabel('CLI client', { exact: true }).selectOption('codex');
  assert.match(await prompt.textContent(), /Connect this Codex client/);
  await card.getByLabel('CLI client', { exact: true }).selectOption('claude');
  assert.match(await prompt.textContent(), /Connect this Claude Code client/);
  assert.doesNotMatch(await prompt.textContent(), /VPN|SSH/);
  mcpURL = 'http://127.0.0.1:8088/mcp';
  await card.getByRole('button', { name: 'Terminal & email', exact: true }).click();
  await card.getByRole('button', { name: 'Terminal & email', exact: true }).click();
  await page.waitForFunction(() => document.querySelector('[data-testid="automation-setup-prompt"]')?.textContent.includes('VPN alone does not redirect loopback'));
  assert.match(await prompt.textContent(), /VPN alone does not redirect loopback/);
  await card.getByRole('button', { name: 'Copy setup prompt', exact: true }).click();
  assert.equal(await page.evaluate(() => window.__automationCopied), await prompt.textContent());
  assert.equal(await card.evaluate((element) => element.scrollWidth > element.clientWidth + 1), false);
  const secrets = await page.evaluate(() => Object.values(localStorage).some((value) => value.includes('pb_mcp_ui-fixture')));
  assert.equal(secrets, false, 'PAT must not be cached in browser storage');
  assert.deepEqual(errors, []);
  console.log('Automation UI passed: direct configuration, self-contained two-client/two-language prompts, token lifecycle, clipboard failures, preferences, preview/SMTP compatibility and mobile layout.');
} finally {
  await browser.close();
}
