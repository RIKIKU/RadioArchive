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
// - A26's browser half (issue #112): renaming 1001 and moving Bench's
//   attachment show in the Audit log page; the audit log's own period
//   saves (ADR 0032).
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
		// The notice first: it goes by itself 10 s after it shows (ADR 0050).
		await expect(listener.getByText(NOTICE)).toBeVisible({ timeout: 20_000 });
		await expect(bench).toContainText(/Disconnected since /, { timeout: 20_000 });
		await expect(bench.getByText('Warning', { exact: true })).toBeVisible();
		await expect(bench.getByRole('list', { name: 'Bench: warnings' })).toContainText(/Disconnected for (under a minute|\d+ min)/);
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
		await expect(bench.getByText('Warning', { exact: true })).toBeVisible();
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

// A26, the browser half (issue #112): changes made in the browser are in
// the Audit log page with who, how and when. (The module's half is the
// fixture's made-up entry here, audit.spec.ts's; a real one through the
// module is issue #117's.) Then the audit log's own period (ADR 0032): a
// longer one saves, and a shorter one that deletes nothing saves without
// asking; both are in the log as settings changes.
test('@live A26, the browser half: renaming 1001 and moving an attachment show in the Audit log; the audit period saves', async ({ page }) => {
	await signIn(page, 'admin');
	const tg = '/api/v1/admin/systems/1/talkgroups/1001';
	const att = '/api/v1/admin/recorders/Bench/attachments/ex';
	const start = Date.now();
	try {
		expect((await page.request.patch(tg, { data: { label: 'DISP North' } })).ok()).toBe(true);
		expect((await page.request.post('/api/v1/admin/systems', { data: { id: 2, label: 'EX-2', name: 'Example network two' } })).ok()).toBe(true);
		expect((await page.request.put(att, { data: { system_id: 2 } })).ok()).toBe(true);
	} finally {
		await page.request.put(att, { data: { system_id: 1 } });
		await page.request.delete('/api/v1/admin/systems/2');
		await page.request.patch(tg, { data: { label: 'DISP N' } });
	}
	const table = page.getByRole('table', { name: 'Audit log' });
	await page.goto('/admin/audit?kind=attachment');
	const moved = table.getByRole('row').filter({ hasText: 'Bench / ex moved from EX to EX-2' }).first();
	await expect(moved).toContainText('Fixture Admin');
	await expect(moved).toContainText('Browser');
	const at = Date.parse((await moved.locator('time').getAttribute('datetime'))!);
	expect(at).toBeGreaterThanOrEqual(start - 2000);
	expect(at).toBeLessThanOrEqual(Date.now() + 2000);
	await expect(table.getByRole('row').filter({ hasText: 'Bench / ex moved from EX-2 to EX' }).first()).toBeVisible();
	await page.goto('/admin/audit?kind=talkgroup&thing=1/1001');
	const renamed = table.getByRole('row').filter({ hasText: 'Label DISP N → DISP North' }).first();
	await expect(renamed).toContainText('Fixture Admin');
	await expect(renamed).toContainText('Browser');
	await expect(renamed.locator('time')).toBeVisible();

	const period = async () => ((await (await page.request.get('/api/v1/admin/audit/retention')).json()) as { days: number }).days;
	await page.goto('/admin/settings');
	const days = page.getByRole('textbox', { name: 'Keep audit log entries (days) required' });
	const save = page.getByRole('button', { name: "Save the audit log's retention" });
	await expect(days).toHaveValue('730');
	try {
		await days.fill('1000');
		await save.click();
		await expect(page.locator('p.audit-status')).toHaveText('Saved: audit log entries are kept for 1,000 days.');
		expect(await period()).toBe(1000);
		// Shorter, but nothing is that old: no question.
		await days.fill('730');
		await save.click();
		await expect(page.locator('p.audit-status')).toHaveText('Saved: audit log entries are kept for 730 days.');
		await expect(page.getByRole('region', { name: /^Delete/ })).toHaveCount(0);
	} finally {
		if ((await period()) !== 730) await page.request.put('/api/v1/admin/audit/retention', { data: { days: 730, confirm: true } });
	}
	await page.goto('/admin/audit?kind=retention');
	const set = table.getByRole('row').filter({ hasText: 'Audit log retention' });
	await expect(set.first()).toContainText('Days 1000 → 730');
	await expect(set.nth(1)).toContainText('Days 730 → 1000');
});
