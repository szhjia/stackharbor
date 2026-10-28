// Run against TestBrowserFixture. All API traffic reaches the production
// gateway/session host; only one accepted response is deliberately dropped.
import {readFile, writeFile, mkdir} from 'node:fs/promises';
import {chromium, expect} from '@playwright/test';
import {pathToFileURL} from 'node:url';

export async function checkLiveControl(page, fixture, out) {
  await mkdir(out, {recursive: true});
  const url = (await readFile(`${fixture}/url`, 'utf8')).trim();
  const base = new URL(url).origin;
  const sid = (await readFile(`${fixture}/session-id`, 'utf8')).trim();
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.setViewportSize({width: 1440, height: 1000});
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'], {origin: base});
  await page.goto(url);
  const built = await readFile(new URL('../../../internal/web/dist/index.html', import.meta.url), 'utf8');
  const asset = built.match(/src="([^"]+\.js)"/)[1];
  await expect(page.locator('script[type="module"]')).toHaveAttribute('src', asset);
  await expect(page.getByRole('button', {name: 'Refresh', exact: true})).toBeVisible();
  await expect(page.locator('vite-error-overlay')).toHaveCount(0);
  const inventory = () => page.evaluate(async () => (await (await fetch('/api/v1/inventory')).json()).data);
  await expect.poll(async () => (await inventory()).sessions.some(s => s.identity.session_id === sid)).toBe(true);
  const workspace = `${base}/workspaces/${sid}`;
  const reviewRestart = async () => {
    await page.goto(workspace);
    await page.getByRole('button', {name: 'Actions for cafe/api', exact: true}).click();
    await page.getByRole('menuitem', {name: 'Restart', exact: true}).click();
    await expect(page.getByRole('dialog')).toBeVisible();
    await expect(page.getByRole('dialog').getByText('cafe/api', {exact: true})).toBeVisible();
  };
  await reviewRestart();
  const acceptedResponse = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/operations'));
  await page.getByRole('button', {name: 'Confirm restart', exact: true}).click();
  const accepted = (await (await acceptedResponse).json()).data;
  await expect(page.getByRole('link', {name: accepted.id, exact: true})).toBeVisible();
  await expect.poll(() => page.evaluate(async ({sid, id}) => (await (await fetch(`/api/v1/sessions/${sid}/operations/${id}`)).json()).data?.state, {sid, id: accepted.id})).toBe('succeeded');
  await page.reload();
  await expect(page.getByRole('link', {name: accepted.id, exact: true})).toBeVisible();
  expect(await page.evaluate(id => JSON.parse(sessionStorage.getItem('stackharbor.tracked-operations')).some(op => op.id === id), accepted.id)).toBe(true);
  await page.screenshot({path: `${out}/accepted-after-reload.png`, fullPage: true});

  await page.goto(`${workspace}/logs?target=cafe%2Fapi`);
  await expect(page.locator('.log-lines').getByText('fixture ready', {exact: true}).first()).toBeVisible();
  await expect(page.locator('.log-lines img')).toHaveCount(0);
  await expect(page.getByRole('button', {name: 'Pause following', exact: true})).toBeVisible();
  for (let attempt = 0; attempt < 3; attempt++) {
    await page.reload();
    await expect(page.locator('.log-lines').getByText('fixture ready', {exact: true}).first()).toBeVisible();
    await expect(page.getByRole('button', {name: 'Pause following', exact: true})).toBeVisible();
  }
  await page.getByRole('textbox', {name: 'Search logs'}).fill('fixture');
  await page.getByRole('button', {name: 'Copy visible logs', exact: true}).click();
  await expect(page.getByRole('status').filter({hasText: 'Copied'})).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain('fixture');
  await page.getByRole('textbox', {name: 'Search logs'}).fill('');
  await page.getByRole('button', {name: 'Pause following', exact: true}).click();
  await writeFile(`${fixture}/append-log`, '1');
  await expect(page.getByRole('button', {name: /new entries · Jump to latest/})).toBeVisible();
  await page.getByRole('button', {name: 'Resume following', exact: true}).click();
  await expect(page.locator('.log-lines').getByText('live acceptance appended output', {exact: true})).toBeVisible();
  await writeFile(`${fixture}/disconnect`, '1');
  await expect.poll(() => readFile(`${fixture}/disconnected`, 'utf8').catch(() => ''), {timeout: 10000}).toBe('1');
  await expect.poll(async () => {
    try { return (await inventory()).sessions.some(s => s.identity.session_id === sid && s.available); }
    catch { return false; }
  }, {timeout: 15000}).toBe(true);
  await writeFile(`${fixture}/append-log`, '2');
  await expect(page.locator('.log-lines').getByText('live acceptance appended output', {exact: true})).toHaveCount(2, {timeout: 15000});

  let dropped;
  let submits = 0;
  await page.route('**/api/v1/sessions/*/operations', async route => {
    if (route.request().method() !== 'POST') return route.continue();
    submits++;
    const response = await route.fetch();
    expect(response.ok()).toBe(true);
    dropped = (await response.json()).data;
    await route.abort('connectionreset');
  });
  await reviewRestart();
  await page.getByRole('button', {name: 'Confirm restart', exact: true}).click();
  await expect(page.getByRole('dialog').getByText(/Request outcome unknown/)).toBeVisible();
  await expect(page.getByRole('button', {name: 'Confirm restart', exact: true})).toBeDisabled();
  await page.getByRole('dialog').getByRole('button', {name: 'Inspect operations', exact: true}).click();
  await expect.poll(() => Boolean(dropped?.id)).toBe(true);
  await expect(page.getByRole('link', {name: dropped.id, exact: true})).toBeVisible();
  expect(submits).toBe(1);
  await page.unroute('**/api/v1/sessions/*/operations');
  await page.goto(`${base}/operations?session=${sid}&operation=${dropped.id}&q=${dropped.id}`);
  await page.reload();
  await expect(page.getByRole('link', {name: dropped.id, exact: true})).toBeVisible();

  await writeFile(`${fixture}/close-primary`, '1');
  await expect.poll(async () => (await inventory()).sessions.some(s => s.identity.session_id === sid), {timeout: 15000}).toBe(false);
  await page.reload();
  await expect(page.getByText('Some operation outcomes are unavailable', {exact: true})).toBeVisible();
  await expect(page.getByText('Missing results do not imply success.', {exact: false})).toBeVisible();
  await page.setViewportSize({width: 390, height: 844});
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({path: `${out}/ended-session-mobile.png`, fullPage: true});
  expect(errors).toEqual([]);
  const result = {accepted: accepted.id, dropped: dropped.id, droppedSubmissions: submits, errors,
    checks: ['real plan/submit/result', 'accepted operation reload', 'logs copy/pause/resume', 'SSE reconnect', 'accepted response loss without retry', 'exact operation bookmark', 'ended session unknown outcome', 'mobile overflow']};
  await writeFile(`${out}/result.json`, JSON.stringify(result, null, 2));
  return result;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [fixture, out = '/tmp/stackharbor-live-control'] = process.argv.slice(2);
  if (!fixture) throw Error('Pass the TestBrowserFixture directory');
  const browser = await chromium.launch({headless: true});
  try {
    const page = await browser.newPage();
    try { console.log(await checkLiveControl(page, fixture, out)); }
    catch (error) {
      await mkdir(out, {recursive: true});
      await page.screenshot({path: `${out}/failure.png`, fullPage: true}).catch(() => {});
      await writeFile(`${out}/failure.txt`, `${page.url()}\n${await page.locator('body').innerText()}`);
      throw error;
    }
  } finally { await browser.close(); }
}
