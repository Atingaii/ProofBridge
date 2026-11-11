import fs from 'node:fs';

const app = read('./src/app/App.tsx');
const help = read('./src/app/pages/HelpPage.tsx');
const login = read('./src/app/pages/LoginScreen.tsx');
const sidebar = read('./src/app/components/SidebarContent.tsx');
const orchestration = read('./src/app/pages/OrchestrationWorkspace.tsx');

assert(app.includes("path === '/help'"), 'missing /help route');
assert(app.includes("path === '/hlep'"), 'missing /hlep alias');
assert(app.indexOf('if (isHelpRoute)') < app.indexOf('if (!user)'), 'help must render before authentication');
assertCount(help, 'src="/help/', 17);
assert(help.includes('协作模式：分析、实现、验证逐步收敛'), 'missing collaboration guide');
assert(help.includes('辩论模式：先写可证伪主张，再寻找反例'), 'missing debate guide');
assert(help.includes('形式化验证：让证明义务和证据成为主线'), 'missing formal-proof guide');
assert(help.includes('用更少上下文获得更稳定的编排'), 'missing context guide');
assertCount(help, '<DemoBlock', 4);
assert(help.includes('Follow-up input and native question cards'), 'missing English interaction guide');
assert(help.includes('Dynamic / Bounded rounds'), 'missing execution mode explanation');
assert(help.includes('historical illustrations'), 'old screenshots need an explicit historical label');
assert(help.includes('window.location.origin'), 'help must use the current site origin');
assert(!help.includes('https://sparkon.cn'), 'help links to an obsolete origin');
assert(!help.includes('当前文档适用于 v0.3.40'), 'help claims a stale release version');
assert(login.includes('href="/help"'), 'login help link missing');
assert(sidebar.includes('href="/help"'), 'chat sidebar help link missing');
assert(orchestration.includes('href="/help"'), 'orchestration sidebar help link missing');

// Run against a fixture-only dev or built server without requiring a production
// login. Kept optional so the existing source checks remain usable without a browser.
if (process.env.DOCUMENTATION_TEST_URL) await checkDocumentationInBrowser(process.env.DOCUMENTATION_TEST_URL);

async function checkDocumentationInBrowser(baseURL) {
  const { chromium } = await import('playwright');
  const { default: nodeAssert } = await import('node:assert/strict');
  const { default: path } = await import('node:path');
  const browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    page.setDefaultTimeout(15000);
    const pageErrors = [];
    const unexpectedWrites = [];
    let healthMode = 'ready';
    let hubVersion = 'hub-fixture-separate-from-frontend';
    page.on('pageerror', (error) => pageErrors.push(error.message));
    await page.addInitScript(() => {
      if (!localStorage.getItem('codexBridge.language')) localStorage.setItem('codexBridge.language', 'zh');
      Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async (value) => { globalThis.__documentationCopiedText = value; } } });
    });
    await page.route('**/api/**', async (route) => {
      if (route.request().method() !== 'GET') unexpectedWrites.push(route.request().url());
      await route.fulfill({ status: 401, json: { message: 'Fixture: public documentation needs no account' } });
    });
    await page.route('**/health', async (route) => {
      if (healthMode === 'hang') return;
      if (healthMode === 'network') return route.abort('failed');
      if (healthMode === 'error') return route.fulfill({ status: 503, json: { message: 'Fixture unavailable' } });
      if (healthMode === 'invalid') return route.fulfill({ json: { status: 'ok' } });
      await route.fulfill({ json: { status: 'ok', version: hubVersion, buildTime: 'hub-only-time' } });
    });

    await page.goto(new URL('/help', baseURL).href, { waitUntil: 'domcontentloaded' });
    await page.getByRole('heading', { name: 'ProofBridge 详细使用教程' }).waitFor();
    await page.getByTestId('hub-version').getByText(hubVersion, { exact: true }).waitFor();
    nodeAssert.equal(await page.locator('main figure img').count(), 17);
    nodeAssert.equal(await page.locator('main pre').count(), 4);
    nodeAssert.equal(await page.locator('#account a code').innerText(), new URL(baseURL).origin);
    const buildTime = await page.getByTestId('frontend-build').innerText();
    nodeAssert.match(buildTime, /^\d{4}-\d{2}-\d{2}T.+Z$/);
    nodeAssert.notEqual(buildTime, hubVersion);

    await page.getByRole('button', { name: '切换语言', exact: true }).click();
    await page.getByRole('heading', { name: 'ProofBridge user guide' }).waitFor();
    nodeAssert.equal(await page.title(), 'ProofBridge Help');
    nodeAssert.doesNotMatch(await page.locator('main').innerText(), /[\u3400-\u9fff]/, 'English help must translate the body, not only navigation');
    await page.getByRole('heading', { name: 'Follow-up input and native question cards' }).waitFor();
    await page.getByRole('heading', { name: 'Dynamic / Bounded rounds' }).waitFor();
    for (const section of ['chat', 'collaboration', 'debate', 'formal-proof']) {
      const demo = page.locator(`#${section}`);
      const content = await demo.locator('pre').innerText();
      await demo.getByRole('button', { name: 'Copy', exact: true }).click();
      nodeAssert.equal(await page.evaluate(() => globalThis.__documentationCopiedText), content, `${section} copies the selected language`);
    }
    const screenshotDir = process.env.DESKTOP_FIXES_SCREENSHOT_DIR;
    if (screenshotDir) {
      fs.mkdirSync(screenshotDir, { recursive: true });
      await page.evaluate(() => window.scrollTo(0, 0));
      await page.screenshot({ path: path.join(screenshotDir, 'help-en.png') });
      await page.locator('#live-input').scrollIntoViewIfNeeded();
      await page.screenshot({ path: path.join(screenshotDir, 'help-en-live-input.png') });
    }

    await page.goto(new URL('/updates', baseURL).href, { waitUntil: 'domcontentloaded' });
    await page.getByRole('heading', { name: 'Release notes and build information' }).waitFor();
    await page.getByTestId('hub-version').getByText(hubVersion, { exact: true }).waitFor();
    nodeAssert.equal(await page.title(), 'ProofBridge Release Notes');
    await page.getByText('Latest documented release v0.3.48', { exact: true }).waitFor();
    nodeAssert.equal(await page.locator('article').count(), 16, 'all historical releases remain');
    nodeAssert.equal(await page.getByText('Original release notes (Chinese)', { exact: true }).count(), 16);
    nodeAssert.equal(await page.locator('article[lang="zh-CN"]').count(), 16);
    nodeAssert.doesNotMatch(await page.locator('main > section').first().innerText(), /[\u3400-\u9fff]/, 'English release overview must be translated');
    nodeAssert.equal(await page.getByTestId('frontend-build').innerText(), buildTime);
    if (screenshotDir) await page.screenshot({ path: path.join(screenshotDir, 'updates-en.png') });

    await page.getByRole('button', { name: 'Change language', exact: true }).click();
    await page.getByRole('heading', { name: '更新记录与版本信息' }).waitFor();
    nodeAssert.equal(await page.getByText('Original release notes (Chinese)', { exact: true }).count(), 0);
    await page.getByRole('button', { name: '切换语言', exact: true }).click();
    for (const failure of ['error', 'network', 'invalid', 'hang']) {
      healthMode = failure;
      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.getByTestId('hub-version').getByText('Currently unavailable', { exact: false }).waitFor();
      nodeAssert.equal(await page.getByTestId('frontend-build').innerText(), buildTime, `${failure} preserves frontend identity`);
      nodeAssert.equal(await page.locator('article').count(), 16, `${failure} leaves release notes readable`);
      healthMode = 'ready';
      hubVersion = `hub-after-${failure}`;
      await page.getByRole('button', { name: 'Retry', exact: true }).click();
      await page.getByTestId('hub-version').getByText(hubVersion, { exact: true }).waitFor();
    }
    nodeAssert.deepEqual(unexpectedWrites, [], 'documentation must not send model tasks or mutate data');
    nodeAssert.deepEqual(pageErrors, []);
    console.log('Documentation browser checks passed: languages, 17 figures, 4 demos, release archive, build identity and health recovery.');
  } finally {
    await browser.close();
  }
}

function read(path) {
  return fs.readFileSync(new URL(path, import.meta.url), 'utf8');
}

function assert(value, message) {
  if (!value) throw new Error(message);
}

function assertCount(source, value, expected) {
  const actual = source.split(value).length - 1;
  if (actual !== expected) throw new Error(`expected ${JSON.stringify(value)} ${expected} time(s), found ${actual}`);
}
