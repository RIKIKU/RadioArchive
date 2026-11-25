/// <reference types="node" />
// The admin pages' tests that change what other tests see (issue #111),
// so they run with the playback tests (@live), one at a time:
//
// - A27, the Recorders page half: Bench's client connection dropped shows
//   it disconnected, and the listener's notice; both clear when it's back.
//   A decode rate of 0 the same. (RECORDER_NOTICE_AFTER is 3 s here, not
//   2 min: the page shows a signal at once, the notice after that delay.)
// - A2, the Recorders page half: a call from Bench / other, which isn't
//   attached, shows in R12's words.
// - R88: lengthening retention just saves; shortening asks, naming the
//   count, and saves once confirmed.
import { test, expect, type Page } from '@playwright/test';
import { clientDisconnected, listen, recorderDeaf, sendCalls, tone } from './live.ts';
import { recorderCard, signIn } from './admin.ts';

const NOTICE = "Some talkgroups aren't being received right now.";

async function recorders(page: Page) {
	await signIn(page, 'admin');
	await page.goto('/admin/recorders');
	await expect(recorderCard(page, 'Bench')).toContainText(/Connected since /);
}

test('@live A27: Bench disconnected shows on the Recorders page and the listener, and clears when it reconnects', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	// The Listen page, listening before anyone signs in (the feed answers
	// everyone alike, P3), then the Recorders page in a second tab.
	const listener = page;
	await listen(listener);
	page = await listener.context().newPage();
	await recorders(page);
	const bench = recorderCard(page, 'Bench');
	const back = clientDisconnected();
	try {
		await expect(bench).toContainText(/Disconnected since /, { timeout: 20_000 });
		await expect(bench.locator('.badge')).toHaveText('Warning');
		await expect(bench.getByRole('list', { name: 'Bench: warnings' })).toContainText(/Disconnected for (under a minute|\d+ min)/);
		await expect(listener.getByText(NOTICE)).toBeVisible({ timeout: 20_000 });
	} finally {
		back();
	}
	await expect(bench).toContainText(/Connected since /, { timeout: 60_000 });
	await expect(bench).not.toContainText('Disconnected');
	await expect(listener.getByText(NOTICE)).toHaveCount(0, { timeout: 30_000 });
	await page.close();
});

test('@live A27: a decode rate of 0 shows on the Recorders page, and clears when it decodes again', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	await recorders(page);
	const bench = recorderCard(page, 'Bench');
	const heardAgain = recorderDeaf();
	try {
		await expect(bench.getByRole('list', { name: 'Bench: warnings' })).toContainText('Control channel lost on ex', { timeout: 30_000 });
		await expect(bench.locator('.systems li')).toContainText('Decode rate: 0.0 messages a second');
		await expect(bench.locator('.systems li')).toContainText('Near 0');
		await expect(bench.locator('.badge')).toHaveText('Warning');
	} finally {
		heardAgain();
	}
	await expect(bench.locator('.systems li')).toContainText('Decode rate: 40.0 messages a second', { timeout: 30_000 });
	await expect(bench).not.toContainText('Control channel lost');
});

test('@live A2: a call from Bench / other, not attached, shows in R12\'s words', async ({ page }) => {
	await recorders(page);
	type Rej = { unattached: { recorder: string; short_name: string; calls: number }[] };
	const count = async () =>
		((await (await page.request.get('/api/v1/admin/rejections')).json()) as Rej).unattached.find((u) => u.recorder === 'Bench' && u.short_name === 'other')?.calls ?? 0;
	const before = await count();
	await sendCalls([{ unit: 5301, shortName: 'other', atMs: 200, pcm: [tone] }]);
	await expect.poll(count, { timeout: 30_000 }).toBe(before + 1);
	const n = before + 1;
	await expect(page.getByRole('list', { name: 'Rejected calls' })).toContainText(
		`Bench / other: ${n === 1 ? '1 call' : `${n} calls`} rejected, not attached to a system`,
		{ timeout: 15_000 }
	);
});

test('@live R88: lengthening retention just saves; shortening names the count, and saves once confirmed', async ({ page }) => {
	await signIn(page, 'admin');
	await page.goto('/admin/settings');
	const days = page.getByRole('textbox', { name: 'Retention period (days) required' });
	const retention = async () => ((await (await page.request.get('/api/v1/admin/retention')).json()) as { days: number }).days;
	await expect(days).toHaveValue('30');
	try {
		await days.fill('31');
		await page.getByRole('button', { name: 'Save', exact: true }).click();
		await expect(page.locator('p.status')).toHaveText('Saved: recordings are kept for 31 days.');
		await expect(page.getByRole('region', { name: /^Delete/ })).toHaveCount(0);
		expect(await retention()).toBe(31);

		await days.fill('30');
		await page.getByRole('button', { name: 'Save', exact: true }).click();
		const confirm = page.getByRole('region', { name: /^Delete [\d,]+ recordings?\?$/ });
		await expect(confirm).toBeVisible();
		await expect(confirm).toContainText(/Shortening retention from 31 days to 30 days deletes [\d,]+ recordings?/);
		expect(await retention()).toBe(31);
		await confirm.getByRole('button', { name: 'Shorten to 30 days' }).click();
		await expect(page.locator('p.status')).toHaveText('Saved: recordings are kept for 30 days.');
		expect(await retention()).toBe(30);
	} finally {
		if ((await retention()) !== 30) await page.request.put('/api/v1/admin/retention', { data: { days: 30, confirm: true } });
	}
});
