import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { chromium } from 'playwright';

const base = process.argv[2] || 'http://127.0.0.1:4197';
const screenshots = process.argv[3];
if (screenshots) await fs.mkdir(screenshots, { recursive: true });
const browser = await chromium.launch({ headless: true, args: ['--no-sandbox'] });
try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  const errors = [];
  const apiRequests = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('request', request => {
    if (new URL(request.url()).pathname.startsWith('/api/')) apiRequests.push(request.url());
  });
  // Exercise the public Chinese page with the app's opposite language/theme.
  await page.addInitScript(() => {
    localStorage.setItem('codexBridge.language', 'en');
    localStorage.setItem('codexBridge.theme', 'dark');
  });
  for (const suffix of ['', '/']) {
    const response = await page.goto(`${base}/blog/orchestration-design${suffix}`);
    assert.equal(response.status(), 200);
    await page.locator('.od-page').waitFor();
    assert.match(await page.title(), /可验证的结果/);
    assert.equal(await page.locator('html').getAttribute('lang'), 'zh-CN');
    assert.equal(await page.locator('h1').count(), 1);
    assert.equal(await page.locator('.od-body section').count(), 8);
  }
  for (const img of await page.locator('.od-figure img').all()) {
    await img.scrollIntoViewIfNeeded();
    await img.evaluate(el => el.decode());
    assert.ok(await img.evaluate(el => el.naturalWidth > 1000));
    assert.ok(await img.getAttribute('alt'));
  }
  assert.equal(await page.locator('.od-figure img').count(), 4);
  assert.deepEqual(apiRequests, [], 'public article must not request private APIs');
  for (const width of [1440, 768, 390, 320]) {
    await page.setViewportSize({ width, height: 1000 });
    await page.evaluate(() => window.scrollTo(0, 0));
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), `page overflow at ${width}px`);
    assert.equal(await page.locator('.od-page').evaluate(el => getComputedStyle(el).backgroundColor), 'rgb(255, 255, 255)');
    if (screenshots) await page.screenshot({ path: path.join(screenshots, `article-${width}.png`) });
  }
  const toc = page.getByRole('navigation', { name: '本文目录', exact: true });
  await toc.getByRole('link', { name: /为什么一轮/ }).click();
  assert.equal(new URL(page.url()).hash, '#premise');
  const demo = page.getByTestId('handoff-demo');
  assert.equal(await demo.locator('li').count(), 4);
  const review = demo.getByRole('button', { name: /阶段 4/ });
  await review.focus();
  await page.keyboard.press('Enter');
  assert.equal(await review.getAttribute('aria-pressed'), 'true');
  assert.match(await page.locator('#handoff-detail').innerText(), /原始需求/);
  await demo.getByRole('button', { name: /重新查看/ }).click();
  assert.match(await page.locator('#handoff-detail h4').innerText(), /A · 执行/);
  await demo.getByRole('button', { name: /下一阶段/ }).click();
  assert.match(await page.locator('#handoff-detail h4').innerText(), /B · 补充执行/);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await demo.scrollIntoViewIfNeeded();
  if (screenshots) await page.screenshot({ path: path.join(screenshots, 'article-demo.png') });
  await page.evaluate(() => document.fonts.ready);
  assert.match(await page.locator('h1').evaluate(el => getComputedStyle(el).fontFamily), /^"OpenAI Sans"/);
  for (const weight of [400, 500, 600]) {
    assert.ok(await page.evaluate(async weight => {
      const fonts = await document.fonts.load(`${weight} 18px "OpenAI Sans"`, 'ProofBridge');
      return fonts.length > 0 && fonts.every(font => font.status === 'loaded');
    }, weight), `OpenAI Sans ${weight} must really load`);
  }
  const expectActive = async id => {
    await toc.locator(`[href="#${id}"][aria-current="location"]`).waitFor({ state: 'attached' });
    assert.equal(await toc.locator('[aria-current="location"]').count(), 1);
  };
  const scrollToHeading = async id => {
    await page.evaluate(id => {
      window.scrollTo(0, document.querySelector(`#${id} h2`).getBoundingClientRect().top + window.scrollY - 100);
    }, id);
    await expectActive(id);
  };
  const ids = await page.locator('.od-body section').evaluateAll(es => es.map(e => e.id));
  for (const id of ids) await scrollToHeading(id);
  // Long sections retain their highlight even after the heading leaves the viewport.
  await page.evaluate(() => window.scrollTo(0, document.querySelector('#comparison').getBoundingClientRect().top + window.scrollY + 400));
  await expectActive('comparison');
  for (const id of [...ids].reverse()) await scrollToHeading(id);
  await toc.getByRole('link', { name: '交接与恢复' }).click();
  await expectActive('workspace');
  await page.setViewportSize({ width: 390, height: 844 });
  await scrollToHeading('accounting');
  await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
  await expectActive('references');
  await page.goto(`${base}/blog/orchestration-design#workflow`);
  await page.locator('.od-body').waitFor();
  await expectActive('workflow');
  await page.evaluate(() => window.scrollTo(0, 0));
  await expectActive('premise');
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.evaluate(() => { window.print = () => { window.__articlePrinted = true; }; });
  await page.getByRole('button', { name: '打印' }).click();
  assert.equal(await page.evaluate(() => window.__articlePrinted), true);
  await page.emulateMedia({ media: 'print' });
  assert.equal(await page.locator('.od-header').isVisible(), false);
  assert.equal(await page.locator('.od-body').isVisible(), true);
  if (screenshots) await page.pdf({ path: path.join(screenshots, 'article-print.pdf'), format: 'A4', printBackground: true });
  await page.emulateMedia({ media: 'screen' });
  assert.deepEqual(errors, []);
  // The new public exception must not open the authenticated statistics route.
  await page.route('**/api/me', route => route.fulfill({ status: 401, contentType: 'application/json', body: '{"error":"unauthorized"}' }));
  await page.goto(`${base}/orchestrate/stats`);
  await page.locator('input[type="password"]').first().waitFor();
  assert.equal(await page.locator('.od-page').count(), 0);
  assert.ok(apiRequests.some(url => new URL(url).pathname === '/api/me'));
  console.log('Article public routes, images, 4 responsive widths, keyboard/handoffs, loaded fonts, scroll/anchor tracking, print, and protected stats: PASS');
} finally {
  await browser.close();
}
