/// <reference types="node" />
// The Systems page's tests that change what other tests see (issue #114;
// SPEC.md R12, R21, R22, A2; ADR 0003, 0006, 0034), so they run with the
// playback tests (@live), one at a time, each putting things back:
//
// - A2's path through the page: Bench / other's call is refused and listed
//   under Not attached; attached to EX from there, its next call is filed
//   under EX, and the call refused before stays refused.
// - Auto-create switched on for EX on the page: the next call on 4711
//   creates it, public and unreviewed (A2). There's no deleting a
//   talkgroup, so 4711 is left hidden, with a tag, afterwards; a retry
//   uses the next ID EX doesn't list.
// - A tag's LED colour set on the page shows for its talkgroups with none
//   of their own: fireground's on 1002, in the catalogue and on Recent
//   calls.
import { test, expect, type Page } from '@playwright/test';
import { sendCalls, tone } from './live.ts';
import { signIn } from './admin.ts';

async function systems(page: Page) {
	await signIn(page, 'admin');
	await page.goto('/admin/systems');
	await expect(page.getByRole('table', { name: 'Systems' })).toContainText('Example network');
}

/** Whether a call with this unit on it is among the public recent calls. */
async function heard(page: Page, unit: number): Promise<boolean> {
	const r = await page.request.get(`/api/v1/recent-calls?unit=${unit}&limit=10`);
	return ((await r.json()) as { calls: unknown[] }).calls.length > 0;
}

test('@live A2 through the page: Bench / other attached to EX from Not attached files its next call under EX; the refused one stays refused', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	const [refused, kept] = [5311 + 10 * test.info().retry, 5312 + 10 * test.info().retry];
	await systems(page);
	type Rej = { unattached: { recorder: string; short_name: string; calls: number }[] };
	const count = async () =>
		((await (await page.request.get('/api/v1/admin/rejections')).json()) as Rej).unattached.find((u) => u.recorder === 'Bench' && u.short_name === 'other')?.calls ?? 0;
	const before = await count();
	await sendCalls([{ unit: refused, talkgroup: 1001, shortName: 'other', atMs: 200, pcm: [tone] }]);
	await expect.poll(count, { timeout: 30_000 }).toBe(before + 1);
	const n = before + 1;
	try {
		await page.reload();
		const list = page.getByRole('list', { name: 'Not attached' });
		await expect(list).toContainText(`Bench / other: ${n === 1 ? '1 call' : `${n} calls`} rejected, not attached to a system`);
		await page.getByRole('combobox', { name: 'System for Bench / other' }).selectOption({ label: 'EX' });
		await page.getByRole('button', { name: 'Attach Bench / other' }).click();
		await expect(page.locator('p.status')).toHaveText(
			'Attached Bench / other to EX. Its calls from now on are filed under EX; calls already kept or refused stay as they were.'
		);
		await expect(page.getByRole('table', { name: 'Attachments' }).locator('tbody tr').filter({ hasText: 'Bench / other' })).toContainText('EX');
		await expect(page.getByRole('list', { name: 'Not attached' })).toHaveCount(0);

		await sendCalls([{ unit: kept, talkgroup: 1001, shortName: 'other', atMs: 200, pcm: [tone] }]);
		await expect.poll(() => heard(page, kept), { timeout: 30_000 }).toBe(true);
		expect(await heard(page, refused)).toBe(false);
		expect(await count()).toBe(n);

		// Removing it asks first, then its calls are refused again.
		const r = page.getByRole('table', { name: 'Attachments' }).locator('tbody tr').filter({ hasText: 'Bench / other' });
		await r.getByRole('button', { name: 'More actions: Bench / other' }).click();
		await page.getByRole('button', { name: 'Remove…' }).click();
		await page.getByRole('dialog', { name: "Remove Bench / other's attachment?" }).getByRole('button', { name: 'Remove the attachment' }).click();
		await expect(page.locator('p.status')).toHaveText("Removed Bench / other's attachment. Its calls from now on are refused and counted under Not attached.");
		await expect(page.getByRole('list', { name: 'Not attached' })).toContainText('Bench / other');
	} finally {
		await page.request.delete('/api/v1/admin/recorders/Bench/attachments/other');
	}
});

test('@live A2: auto-create switched on for EX on the page; the next call on 4711 creates it, public and unreviewed', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	await systems(page);
	type Row = { system_id: number; id: number; state: string; unreviewed: boolean; tag: string | null };
	const table = async () => ((await (await page.request.get('/api/v1/admin/talkgroups')).json()) as { talkgroups: Row[] }).talkgroups;
	// 4711, unless an earlier try (or another browser, sharing a database
	// when run by hand) left it: then the next one EX doesn't list.
	const listed = new Set((await table()).filter((t) => t.system_id === 1).map((t) => t.id));
	let tg = 4711;
	while (listed.has(tg)) tg++;
	const row = async () => (await table()).find((t) => t.system_id === 1 && t.id === tg);
	try {
		await page.getByRole('button', { name: 'Edit system EX' }).click();
		const edit = page.getByRole('region', { name: 'Edit system EX' });
		await edit.getByRole('checkbox', { name: 'Auto-create talkgroups' }).check();
		await edit.getByRole('button', { name: 'Save' }).click();
		await edit.getByRole('region', { name: 'Switch auto-create on for EX?' }).getByRole('button', { name: 'Switch auto-create on' }).click();
		await expect(page.locator('p.status')).toHaveText('Saved system EX: auto-create is on.');
		await expect(page.getByRole('table', { name: 'Systems' }).locator('tbody tr').first()).toContainText('On');

		await sendCalls([{ unit: 5321 + test.info().retry, talkgroup: tg, atMs: 200, pcm: [tone] }]);
		await expect.poll(async () => (await row())?.state, { timeout: 30_000 }).toBe('public');
		const made = (await row())!;
		expect(made.unreviewed).toBe(true);
		expect(made.tag).toBeNull();
	} finally {
		await page.request.patch('/api/v1/admin/systems/1', { data: { auto_create: false } });
		if (await row()) await page.request.patch(`/api/v1/admin/systems/1/talkgroups/${tg}`, { data: { tag: 'dispatch', state: 'hidden' } });
	}
	expect(((await (await page.request.get('/api/v1/admin/systems/1')).json()) as { auto_create: boolean }).auto_create).toBe(false);
});

test("@live a tag's LED colour set on the page shows for its talkgroups with none of their own: fireground's on 1002", async ({ page }) => {
	await systems(page);
	type Cat = { talkgroups: { id: number; led_colour: string | null }[] };
	const led = async () => ((await (await page.request.get('/api/v1/catalogue')).json()) as Cat).talkgroups.find((t) => t.id === 1002)?.led_colour ?? null;
	const own = ((await (await page.request.get('/api/v1/admin/systems/1/talkgroups/1002')).json()) as { led_colour: string | null }).led_colour;
	expect(own).toBeNull();
	const tags = (await (await page.request.get('/api/v1/admin/tags')).json()) as { id: number; name: string }[];
	const id = tags.find((t) => t.name === 'fireground')!.id;
	try {
		await page.getByRole('button', { name: 'Edit tag fireground' }).click();
		const edit = page.getByRole('region', { name: 'Edit tag fireground' });
		await edit.getByRole('combobox', { name: 'LED colour' }).selectOption('cyan');
		await edit.getByRole('button', { name: 'Save' }).click();
		await expect(page.locator('p.status')).toHaveText('Saved tag fireground.');
		const r = page.getByRole('table', { name: 'Tags' }).locator('tbody tr').filter({ hasText: 'fireground' });
		await expect(r).toContainText('cyan');
		await expect(r.locator('.led[data-led="cyan"]')).toHaveCount(1);
		await expect.poll(led).toBe('cyan');
		await page.goto('/recent?talkgroups=1002');
		await expect(page.locator('main .led[data-led="cyan"]').first()).toBeVisible({ timeout: 15_000 });
	} finally {
		await page.request.patch(`/api/v1/admin/tags/${id}`, { data: { led_colour: null } });
	}
	expect(await led()).toBeNull();
});
