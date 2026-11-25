/// <reference types="node" />
// The Talkgroups page's test that changes what a listener hears (issue
// #113; SPEC.md R30, R31; A11's path), so it runs with the playback tests
// (@live), one at a time: hiding 1001 from its row withdraws its live call
// from a listener and takes it out of the catalogue at once (the
// catalogue's generation, ADR 0006).
import { test, expect } from '@playwright/test';
import { firstOf, heard, listen, seconds, sendCalls, setTalkgroup } from './live.ts';
import { signIn } from './admin.ts';

test('@live hiding 1001 from its row withdraws its live call from a listener and takes it out of the catalogue at once', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	// The Listen page, listening before anyone signs in, then the
	// Talkgroups page in a second tab.
	const listener = page;
	await listen(listener);
	const adminPage = await listener.context().newPage();
	await signIn(adminPage, 'admin');
	await adminPage.goto('/admin/talkgroups');
	const more = adminPage.getByRole('button', { name: 'More actions: 1001 Dispatch North' });
	await expect(more).toBeVisible();

	const sending = sendCalls([{ unit: 5291, atMs: 200, pcm: seconds(8) }]);
	try {
		const live = await firstOf(listener, 5291, 'live');
		await more.click();
		const hide = adminPage.getByRole('button', { name: 'Hide', exact: true });
		const asked = Date.now();
		// Hiding doesn't ask: it only takes away, and is undone in a click.
		await hide.click();
		await expect(adminPage.locator('p.status')).toHaveText('1001 Dispatch North is hidden now.');
		await expect(adminPage.getByRole('dialog')).toHaveCount(0);
		await expect.poll(async () => (await heard(listener)).ended.filter((e) => e.how === 'withdrawn' && e.call_id === live.call_id).length, { timeout: 5_000 }).toBe(1);
		const withdrawn = (await heard(listener)).ended.find((e) => e.call_id === live.call_id)!;
		console.log(`hiding from the row: withdrawn ${withdrawn.at - asked} ms after the press`);
		expect(withdrawn.at - asked, 'withdrawn within 2 s of the press').toBeLessThanOrEqual(2_000);
		// Out of the catalogue at once.
		const catalogue = await (await listener.request.get('/api/v1/catalogue')).text();
		expect(catalogue).not.toContain('Dispatch North');
		expect(catalogue).not.toContain('DISP N');
		// The row says so; making it public again asks first.
		await expect(adminPage.locator('main table tbody tr').filter({ hasText: 'Dispatch North' })).toContainText('Hidden');
		await more.click();
		await adminPage.getByRole('button', { name: 'Make public…' }).click();
		await adminPage.getByRole('dialog', { name: 'Make 1001 Dispatch North public?' }).getByRole('button', { name: 'Make 1001 Dispatch North public' }).click();
		await expect(adminPage.locator('p.status')).toHaveText('1001 Dispatch North is public now.');
		expect(await (await listener.request.get('/api/v1/catalogue')).text()).toContain('Dispatch North');
		await sending;
	} finally {
		await setTalkgroup(1001, 'state', 'public');
		await sending.catch(() => undefined);
	}
});
