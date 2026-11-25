/// <reference types="node" />
// SPEC.md section 9, steps 2 and 3, in a browser, on an install made from
// the release's files behind the deploy guide's nginx example
// (scripts/ci/e2e-section9.sh, issue #155): the first admin signs up and is
// made admin, creates system EX, imports the fixture's setup and attaches
// the recorder `recorder add` made; then a listener presses Start listening
// with {1001} and hears a fixture call live within 2 s, replays it from
// Recent calls, shares it and downloads a basket. The fake recorder plays
// trunk-recorder's part (live.ts's sendCalls).
import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { firstOf, listen, playedFrom, seconds, sendCalls, dock, env } from '../tests/live.ts';

const MAILPIT = env('RA_MAILPIT_URL');
const ADMIN = env('RA_ADMIN_EMAIL');
const RECORDER = env('RA_RECORDER');
const password = 'correct horse first admin staple';

async function mail(request: APIRequestContext, to: string, since: Date): Promise<string> {
	const deadline = Date.now() + 90_000;
	while (Date.now() < deadline) {
		const res = await request.get(`${MAILPIT}/api/v1/search`, { params: { query: `to:"${to}"` } });
		const seen = ((await res.json()) as { messages: { ID: string; Created: string }[] }).messages;
		const m = seen.find((m) => new Date(m.Created) > since);
		if (m) return ((await (await request.get(`${MAILPIT}/api/v1/message/${m.ID}`)).json()) as { Text: string }).Text;
		await new Promise((r) => setTimeout(r, 500));
	}
	throw new Error(`no email to ${to}`);
}

async function answer(page: Page, fields: Record<string, string>) {
	const first = page.locator(`input[name="${Object.keys(fields)[0]}"]`);
	await expect(first).toBeVisible({ timeout: 60_000 });
	await expect(page.locator('ak-loading-overlay')).toHaveCount(0, { timeout: 30_000 });
	for (const [name, value] of Object.entries(fields)) await page.locator(`input[name="${name}"]`).fill(value);
	await page.locator('button[type="submit"]').click();
}

test.describe.configure({ mode: 'serial' });

test('step 2: the first admin signs up, creates EX, imports the setup and attaches the recorder', async ({ page, request }) => {
	await page.goto('/');
	await page.getByRole('button', { name: 'Account menu, signed out' }).click();
	await page.getByRole('link', { name: 'Sign in' }).click();
	await page.getByRole('link', { name: /Sign up/ }).click();
	const since = new Date(Date.now() - 2000);
	await answer(page, { email: ADMIN, name: 'Made Up Admin', password, password_repeat: password });
	const code = /\b\d{6}\b/.exec(await mail(request, ADMIN, since))?.[0];
	expect(code, 'a code in the email').toBeTruthy();
	await answer(page, { code: code! });
	await expect(page.getByRole('button', { name: /^Account menu, / })).not.toHaveAccessibleName('Account menu, signed out', { timeout: 60_000 });
	const me = (await (await page.request.get('/api/v1/account')).json()) as { account?: { admin?: boolean } };
	expect(me.account?.admin, 'INITIAL_ADMIN_EMAIL is made the first admin').toBe(true);

	const api = (method: string, path: string, data?: unknown) =>
		page.request.fetch(`/api/v1/admin${path}`, { method, data, headers: { Origin: new URL(page.url()).origin } });
	expect((await api('POST', '/systems', { id: 1, label: 'EX', name: 'Example network' })).status()).toBe(201);
	const setup = JSON.parse(readFileSync(env('RA_FIXTURE_SETUP'), 'utf8'));
	const preview = await api('POST', '/import', { dry_run: true, setup });
	expect(preview.status()).toBe(200);
	const { plan } = (await preview.json()) as { plan: string };
	expect((await api('POST', '/import', { plan, setup })).status()).toBe(200);
	expect((await api('PUT', `/recorders/${encodeURIComponent(RECORDER)}/attachments/ex`, { system_id: 1 })).status()).toBe(200);

	await page.goto('/admin/talkgroups');
	await expect(page.getByRole('main')).toContainText('Dispatch North');
	await page.goto('/admin/recorders');
	await expect(page.getByRole('main')).toContainText(RECORDER);
});

test('step 3: a listener hears a call live within 2 s, replays it, shares it and downloads a basket', async ({ page, context }) => {
	await context.grantPermissions(['clipboard-read', 'clipboard-write']);
	await listen(page);
	const sent = await sendCalls([{ unit: 5001, atMs: 500, pcm: seconds(3) }]);
	const first = await firstOf(page, 5001, 'live');
	const delay = first.at - sent.get(5001)!;
	console.log(`section 9: the first frame decoded ${delay} ms after the fake recorder sent its first PCM`);
	expect(delay, 'the delay budget (R38)').toBeLessThan(2000);
	await playedFrom(page, 5001, 'live', 60_000);

	// Replay it from Recent calls, once its recording is in.
	await page.goto('/recent');
	const row = page.getByRole('listitem').filter({ hasText: 'Dispatch North' }).first();
	await expect(row).toBeVisible({ timeout: 60_000 });
	await row.getByRole('button', { name: /^Play: Dispatch North/ }).first().click();
	await expect(dock(page)).toContainText('Dispatch North');

	// Share it: the call's link opens its card.
	await row.getByRole('button', { name: /^Share: / }).first().click();
	const link = await page.evaluate(() => navigator.clipboard.readText());
	expect(link).toMatch(/\/calls\/[0-9a-f-]{36}$/);
	await page.goto(link);
	await expect(page.getByRole('main')).toContainText('Dispatch North');

	// Into the basket, and downloaded as a zip.
	await page.goto('/recent');
	await page.getByRole('listitem').filter({ hasText: 'Dispatch North' }).first().getByRole('button', { name: /^Add to basket: / }).first().click();
	await page.goto('/basket');
	const download = page.waitForEvent('download', { timeout: 60_000 });
	await page.getByRole('group', { name: /^Download / }).getByRole('link').first().click();
	const file = await download;
	expect(file.suggestedFilename()).toMatch(/\.zip$/);
	const size = (await readFileSync(await file.path())).length;
	expect(size, 'a zip with the call in it').toBeGreaterThan(1000);
});
