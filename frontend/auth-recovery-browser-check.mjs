import assert from 'node:assert/strict';
import { chromium } from 'playwright';
const baseURL = process.env.SITE_LOADING_TEST_URL || 'http://127.0.0.1:37638';
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const until = async (check, message) => { const deadline = Date.now() + 30000; while (!await check()) { assert.ok(Date.now() < deadline, message); await sleep(30); } };
const gate = () => { let release; const promise = new Promise(resolve => { release = resolve; }); return { promise, release }; };
const browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
try {
  const page = await browser.newPage();
  page.setDefaultTimeout(30000);
  await page.clock.install();
  const errors = [];
  let mode = '503', reads = 0, active = 0, maxActive = 0, adminReads = 0, held = null;
  page.on('pageerror', error => errors.push(error.message));
  await page.addInitScript(() => {
    localStorage.setItem('codexBridge.language', 'zh');
    window.__visible = true; window.__online = true;
    Object.defineProperty(document, 'visibilityState', { get: () => window.__visible ? 'visible' : 'hidden' });
    Object.defineProperty(navigator, 'onLine', { get: () => window.__online });
  });
  await page.route('**/api/**', async route => {
    assert.equal(route.request().method(), 'GET', 'auth recovery must never write or submit tasks');
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/me') {
      reads++; active++; maxActive = Math.max(maxActive, active);
      const responseMode = mode;
      if (held) await held.promise;
      active--;
      await route.fulfill(responseMode === 'ok' ? { json: { user: { id: 'fixture', username: 'fixture', isAdmin: true } } } : { status: Number(responseMode), json: { message: 'Temporarily unavailable' } }).catch(() => {});
    } else {
      if (path === '/api/admin/usage') adminReads++;
      await route.fulfill({ json: {} });
    }
  });
  const alert = page.getByRole('alert');
  const retry = page.getByRole('button', { name: '重试', exact: true });
  const events = async (visible, online) => page.evaluate(({ visible, online }) => {
    window.__visible = visible; window.__online = online;
    document.dispatchEvent(new Event('visibilitychange')); window.dispatchEvent(new Event(online ? 'online' : 'offline'));
  }, { visible, online });
  const originalURL = `${baseURL}/admin/usage?keep=original`;
  await page.goto(originalURL, { waitUntil: 'domcontentloaded' });
  await alert.waitFor(); assert.equal(reads, 1);
  held = gate();
  await page.clock.runFor(2100); await until(() => reads === 2, 'first automatic retry missing');
  assert.equal(await alert.isVisible(), true, 'retry must preserve error card');
  assert.equal(await retry.isDisabled(), true);
  for (let i = 0; i < 5; i++) await events(true, true);
  await page.clock.runFor(5000); assert.equal(reads, 2, 'events must not overlap an active request');
  held.release(); held = null;
  await until(() => retry.isEnabled(), 'retry did not settle');
  await events(false, true); await page.clock.runFor(60000); assert.equal(reads, 2, 'hidden tab retried');
  await events(true, false); await page.clock.runFor(60000); assert.equal(reads, 2, 'offline tab retried');
  mode = 'ok'; await events(true, true); await page.clock.runFor(10);
  await until(() => adminReads > 0, 'online recovery did not restore original page');
  assert.equal(page.url(), originalURL); assert.equal(maxActive, 1);
  const recoveredReads = reads; await page.clock.runFor(60000); assert.equal(reads, recoveredReads, 'successful auth continued probing');

  // Persistent failures keep a capped backoff, rather than stopping permanently.
  mode = '503'; await page.reload({ waitUntil: 'domcontentloaded' }); await alert.waitFor();
  for (const delay of [2000, 5000, 10000, 30000, 30000]) {
    const before = reads;
    await page.clock.runFor(delay + 100);
    await until(async () => reads === before + 1 && await retry.isEnabled(), 'bounded backoff did not advance once');
  }

  // A transient 503 turning into genuine 401 must stop recovery entirely.
  mode = '503'; await page.reload({ waitUntil: 'domcontentloaded' }); await alert.waitFor();
  mode = '401'; const before401 = reads; await retry.click();
  await until(() => reads === before401 + 1, 'manual retry missing');
  await page.getByRole('textbox', { name: /用户名|Username/ }).waitFor();
  const deniedReads = reads;
  await events(true, true); await page.clock.runFor(60000); assert.equal(reads, deniedReads, '401 must not auto-retry');
  assert.equal(page.url(), originalURL, '401 must preserve requested URL');

  // An initial timeout is recoverable without a click, including a late old response.
  mode = 'ok'; held = gate(); const beforeTimeout = reads;
  await page.reload({ waitUntil: 'domcontentloaded' });
  await until(() => reads > beforeTimeout, 'timeout fixture did not start');
  await page.clock.runFor(12100); await alert.waitFor();
  held.release(); held = null;
  await page.clock.runFor(2100);
  await until(() => reads > beforeTimeout + 1, 'timeout did not automatically retry');
  await alert.waitFor({ state: 'hidden' });
  assert.equal(page.url(), originalURL);
  assert.deepEqual(errors, []);
  console.log('Auth recovery passed: automatic 503/timeout recovery, stable card, single request, hidden/offline pause, event recovery, manual retry, terminal 401 and original URL.');
} finally { await browser.close(); }
