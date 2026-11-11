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
  const errors = [], apiRequests = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('request', request => { if (new URL(request.url()).pathname.startsWith('/api/')) apiRequests.push(request.url()); });
  await page.addInitScript(() => {
    localStorage.setItem('codexBridge.language', 'en');
    localStorage.setItem('codexBridge.theme', 'dark');
  });
  for (const suffix of ['', '/']) {
    assert.equal((await page.goto(`${base}/blog/orchestration-design${suffix}`)).status(), 200);
    await page.locator('.pb-essay').waitFor();
    assert.match(await page.title(), /执行与审查/);
    assert.equal(await page.locator('html').getAttribute('lang'), 'zh-CN');
    assert.equal(await page.locator('h1').count(), 1);
  }
  const images = page.locator('.pb-essay img');
  assert.equal(await images.count(), 4);
  for (const img of await images.all()) {
    await img.scrollIntoViewIfNeeded();
    await img.evaluate(el => el.decode());
    assert.ok(await img.evaluate(el => el.naturalWidth > 0));
    assert.ok(await img.getAttribute('alt'));
  }
  assert.deepEqual(apiRequests, [], 'public mainline article must not request private APIs');
  for (const width of [1440, 768, 390, 320]) {
    await page.setViewportSize({ width, height: 1000 });
    await page.evaluate(() => window.scrollTo(0, 0));
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), `page overflow at ${width}px`);
    if (screenshots) await page.screenshot({ path: path.join(screenshots, `mainline-article-${width}.png`) });
  }
  await page.setViewportSize({ width: 1440, height: 1000 });
  const review = page.getByRole('tab', { name: /审查/ });
  await review.focus();
  await page.keyboard.press('Enter');
  assert.equal(await review.getAttribute('aria-selected'), 'true');
  assert.match(await page.locator('.pb-phase-detail').innerText(), /原始验收条件/);
  await page.getByRole('tab', { name: /执行/, exact: false }).first().click();
  assert.match(await page.locator('.pb-phase-detail').innerText(), /候选实现/);
  const turnMode = page.locator('.pb-mode-switch button').last();
  await turnMode.click();
  assert.ok((await turnMode.getAttribute('class')).includes('is-active'));
  await page.locator('.pb-toc a[href="#workflow"]').click();
  assert.equal(new URL(page.url()).hash, '#workflow');
  await page.route('**/api/me', route => route.fulfill({ status: 401, json: { error: 'unauthorized' } }));
  await page.goto(`${base}/orchestrate/stats`);
  await page.locator('input[type="password"]').first().waitFor();
  assert.equal(await page.locator('.pb-essay').count(), 0);
  assert.ok(apiRequests.some(url => new URL(url).pathname === '/api/me'));
  assert.deepEqual(errors, []);
  console.log('Mainline public article, images, 4 responsive widths, keyboard phase controls, anchors and protected stats: PASS');
} finally {
  await browser.close();
}
