/// <reference types="node" />
// Quiet talkgroups (SPEC.md R114-R117, A44-A47; ADR 0054) in the browser:
// the Listen page's tiles, counts and chips leave them out, the one search
// finds one only by its exact number, name or label unless "Include quiet
// talkgroups" is ticked, a listening set keeps them, Recent calls' filter
// leaves them out, and the admin pages show everything, quiet ones
// badged, with the window on the Settings page. The listener's pages run
// on a made-up catalogue like a deployment's whose recorder hears one
// site: 1,210 talkgroups, most never heard.
import { test, expect, type Page } from '@playwright/test';
import { axe, inLook, ownLook, saveLook, signIn, themes, widths } from './admin.ts';
import { heard, indexOf, keyOf, quietNote, searchShown, type Catalogue, type Talkgroup } from '../src/lib/listening/set.ts';

const SAVED = 'radioarchive.listening-set';

function talkgroup(id: number, group: string, tag: string, quiet?: number): Talkgroup {
	const t: Talkgroup = { id, label: `TG${id}`, name: `Channel ${id}`, tag, groups: [group], led_colour: null, alert: null, delay_minutes: 0, fireground_channel: null, quiet: quiet !== undefined };
	if (quiet !== undefined) t.not_heard_days = quiet;
	return t;
}
const range = (from: number, to: number, f: (id: number) => Talkgroup) => Array.from({ length: to - from + 1 }, (_, i) => f(from + i));

// Harbour, where the recorder is: 101-130 heard, 131-140 not for 12 days,
// and fireground 501-510 (its tag shown always). Ridge: 2001-2400, never
// heard. Valley: 3001-3020 heard, 3021-3760 never.
const catalogue: Catalogue & { time_zone: string } = {
	time_zone: 'Australia/Melbourne',
	quiet_window_days: 7,
	groups: ['Harbour', 'Ridge', 'Valley'],
	tags: ['dispatch', 'tactical', 'fireground'],
	talkgroups: [
		...range(101, 140, (id) => talkgroup(id, 'Harbour', id % 3 ? 'dispatch' : 'tactical', id > 130 ? 12 : undefined)),
		...range(501, 510, (id) => talkgroup(id, 'Harbour', 'fireground')),
		...range(2001, 2400, (id) => talkgroup(id, 'Ridge', 'dispatch', 7)),
		...range(3001, 3760, (id) => talkgroup(id, 'Valley', id % 2 ? 'dispatch' : 'tactical', id > 3020 ? 7 : undefined))
	]
};
const SHOWN = 30 + 10 + 20;
const QUIET = 10 + 400 + 740;

async function open(page: Page, set: { id: number; priority?: boolean }[] = [], path = '/') {
	await page.route('**/api/v1/catalogue', (r) => r.fulfill({ json: catalogue }));
	await page.addInitScript(
		([k, s]) => {
			if (sessionStorage.getItem('seeded')) return;
			sessionStorage.setItem('seeded', '1');
			localStorage.setItem(k as string, JSON.stringify({ v: 1, talkgroups: s }));
		},
		[SAVED, set] as const
	);
	await page.goto(path);
}

const summary = (page: Page) => page.getByRole('status').filter({ hasText: 'talkgroups on' });
const searchBox = (page: Page) => page.getByRole('searchbox', { name: 'Search talkgroups' });
const results = (page: Page) => page.getByRole('list', { name: 'Search results' }).getByRole('listitem');
const saved = (page: Page) => page.evaluate((k) => (JSON.parse(localStorage.getItem(k) ?? 'null')?.talkgroups ?? []) as { id: number }[], SAVED);

// The rules on their own, as set.ts has them.
test('the search leaves quiet talkgroups out but for an exact number, name or label; a live call brings one back', () => {
	const ix = indexOf(catalogue);
	expect(ix.shown).toHaveLength(SHOWN);
	expect(ix.quiet).toBe(QUIET);
	const ids = (ts: Talkgroup[]) => ts.map((t) => t.id);
	expect(ids(searchShown(ix, 'channel 13'))).toEqual([130]);
	expect(ids(searchShown(ix, 'channel 13', true))).toEqual(range(130, 139, (id) => talkgroup(id, '', '')).map((t) => t.id));
	expect(ids(searchShown(ix, '2005'))).toEqual([2005]);
	expect(ids(searchShown(ix, 'Channel 2005'))).toEqual([2005]);
	expect(ids(searchShown(ix, 'tg2005'))).toEqual([2005]);
	expect(ids(searchShown(ix, '200'))).toEqual([]);
	expect(quietNote(ix.byKey.get('2005')!)).toBe('Not heard in 7 days');
	expect(quietNote(ix.byKey.get('131')!)).toBe('Not heard in 12 days');
	expect(quietNote(ix.byKey.get('101')!)).toBeUndefined();

	const after = heard(ix, keyOf({ id: 2005 }));
	expect(after.byKey.get('2005')!.quiet).toBe(false);
	expect(after.shown).toHaveLength(SHOWN + 1);
	expect(heard(after, '2005')).toBe(after);
	expect(heard(ix, '101')).toBe(ix);
});

test('A44: the tiles and counts leave quiet talkgroups out; a tile switches its quiet ones too, which stay on unseen', async ({ page }) => {
	await open(page);
	await expect(summary(page)).toHaveText(`0 of ${SHOWN} talkgroups on`);
	const agencies = page.getByRole('list', { name: 'Agencies' });
	// Ridge has only quiet talkgroups: no tile.
	await expect(agencies.getByRole('button')).toHaveCount(2);
	const harbour = agencies.getByRole('button', { name: /^Harbour/ });
	await expect(harbour).toContainText('40 off');
	await expect(agencies.getByRole('button', { name: /^Valley/ })).toContainText('20 off');

	await harbour.click();
	await expect(harbour).toHaveAttribute('aria-pressed', 'true');
	await expect(harbour).toContainText('All 40 on');
	await expect(summary(page)).toHaveText(`40 of ${SHOWN} talkgroups on`);
	// The set holds Harbour's 10 quiet talkgroups as well: they play when heard.
	expect((await saved(page)).map((e) => e.id).sort((a, b) => a - b)).toEqual([...range(101, 140, (id) => talkgroup(id, '', '')), ...range(501, 510, (id) => talkgroup(id, '', ''))].map((t) => t.id));
	await expect(page.getByRole('heading', { name: 'Talkgroups on (40)' })).toBeVisible();
	await expect(page.getByText('Also on: 10 quiet talkgroups, not heard in 7 days.')).toBeVisible();

	// Off again takes the quiet ones out too.
	await harbour.click();
	expect(await saved(page)).toEqual([]);
});

test('the one search: a near term finds no quiet talkgroup, an exact one does, marked, and the filter puts them back', async ({ page }) => {
	await open(page, [{ id: 2005 }]);
	await expect(summary(page)).toHaveText(`0 of ${SHOWN} talkgroups on`);
	// A set naming a quiet talkgroup keeps it, unseen.
	await expect(page.getByText('Also on: 1 quiet talkgroup, not heard in 7 days.')).toBeVisible();

	await searchBox(page).fill('channel 13');
	await expect(results(page)).toHaveCount(1);
	await expect(results(page).first()).toContainText('Channel 130');

	const include = page.getByRole('checkbox', { name: /Include quiet talkgroups/ });
	await expect(include).toBeVisible();
	await expect(page.getByText(`${QUIET.toLocaleString('en-AU')} not heard in 7 days`)).toBeVisible();
	await include.check();
	await expect(results(page)).toHaveCount(10);
	await expect(results(page).nth(1)).toContainText('Not heard in 12 days');
	await include.uncheck();

	await searchBox(page).fill('2400');
	await expect(results(page)).toHaveCount(1);
	await expect(results(page).first()).toContainText('Channel 2400');
	await expect(results(page).first()).toContainText('Not heard in 7 days');
	// It's switched on from there like any other.
	await results(page).first().getByRole('button', { name: /^Channel 2400/ }).click();
	expect((await saved(page)).map((e) => e.id)).toEqual([2005, 2400]);
});

test('Recent calls: the talkgroup filter suggests no quiet talkgroup but for an exact match', async ({ page }) => {
	await open(page, [], '/recent');
	const toggle = page.getByRole('button', { name: 'Filters', exact: true });
	if ((await toggle.getAttribute('aria-expanded')) !== 'true') await toggle.click();
	await page.getByRole('radio', { name: 'These talkgroups' }).check();
	const add = page.getByRole('combobox', { name: 'Add a talkgroup' });
	await add.fill('Channel 13');
	await expect(page.getByRole('listbox', { name: 'Talkgroups' }).getByRole('option')).toHaveCount(1);
	await expect(page.getByRole('option', { name: /Channel 130 \(130\)/ })).toBeVisible();
	await add.fill('2005');
	await expect(page.getByRole('option', { name: /Channel 2005 \(2005\) · Not heard in 7 days/ })).toBeVisible();
});

for (const theme of themes)
	for (const width of widths)
		test(`axe: the Listen page with quiet talkgroups, ${theme}, ${width}px`, async ({ page, context, baseURL }) => {
			// Three axe passes over a page of a thousand talkgroups, beside other tests.
			test.setTimeout(120_000);
			await saveLook(context, baseURL!, 'harbour', theme);
			await page.setViewportSize({ width, height: 900 });
			await open(page, [...range(101, 130, (id) => talkgroup(id, '', '')).map((t) => ({ id: t.id })), { id: 131 }, { id: 2005 }]);
			await inLook(page, 'harbour', theme);
			await expect(summary(page)).toBeVisible();
			await axe(page, width);
			await searchBox(page).fill('2005');
			await expect(results(page)).toHaveCount(1);
			await axe(page, width);
			await searchBox(page).fill('channel 13');
			await page.getByRole('checkbox', { name: /Include quiet talkgroups/ }).check();
			await expect(results(page)).toHaveCount(10);
			await axe(page, width);
		});

// The admin pages, with the admin table's answer made like a deployment's
// in the browser: every talkgroup shown, quiet ones badged.
function quietRows() {
	const now = Date.now();
	return catalogue.talkgroups.slice(0, 300).map((t, i) => ({
		system_id: 1, id: 9000 + i, label: t.label, name: t.name, tag: t.tag, groups: t.groups, state: 'public', unreviewed: false,
		delay_minutes: 0, alert: null, led_colour: null, sort_order: 0, fireground_channel: null, type: '', frequency: null,
		last_heard_at: t.quiet ? (t.not_heard_days === 12 ? new Date(now - 12.5 * 86_400_000).toISOString() : null) : new Date(now - 3_600_000).toISOString(),
		calls_30_days: t.quiet ? 0 : 40 + i, always_show: t.tag === 'fireground', quiet: !!t.quiet, not_heard_days: t.not_heard_days
	}));
}

async function adminTable(page: Page) {
	await page.route('**/api/v1/admin/talkgroups', async (route) => {
		const r = await route.fetch();
		const body = (await r.json()) as { talkgroups: object[] };
		body.talkgroups = [...body.talkgroups, ...quietRows()];
		await route.fulfill({ response: r, json: body });
	});
	await ownLook(page);
	await signIn(page, 'admin');
	await page.goto('/admin/talkgroups');
	await expect(page.getByRole('status').filter({ hasText: /^Showing/ })).toHaveText(/^Showing [\d,]+ of [\d,]+ talkgroups\.$/, { timeout: 30_000 });
}

test('the Talkgroups page shows every talkgroup, quiet ones badged, with a Quiet only filter and Always show in the editor', async ({ page }) => {
	await adminTable(page);
	const quietBadges = page.getByRole('table', { name: 'Talkgroups' }).getByText('Quiet', { exact: true });
	await expect(quietBadges.first()).toBeVisible();
	await page.getByRole('checkbox', { name: 'Quiet only' }).check();
	// Of the 300 added: Harbour's 10 and Ridge's first 250.
	await expect(page.getByRole('status').filter({ hasText: /^Showing/ })).toHaveText(/^Showing 260 of [\d,]+ talkgroups\.$/);
	await page.getByRole('checkbox', { name: 'Quiet only' }).uncheck();
	// The fixture's 1001 has Always show in its editor, off.
	await page.getByRole('button', { name: /^Edit 1001 / }).click();
	await expect(page.getByRole('checkbox', { name: 'Always show' })).not.toBeChecked();
	await page.getByRole('button', { name: 'Cancel' }).click();
});

test('@live the Settings page sets the quiet window, and says how long each system has received towards it', async ({ page }) => {
	await ownLook(page);
	await signIn(page, 'admin');
	await page.goto('/admin/settings');
	const field = page.getByRole('textbox', { name: /Quiet after \(days\)/ });
	await expect(field).toHaveValue('7');
	await expect(page.getByText(/^Nothing of a system is quiet until it has received for a whole window: EX, [\d.]+ of 7 days so far/)).toBeVisible();
	await field.fill('91');
	await page.getByRole('button', { name: 'Save the quiet window' }).click();
	await expect(page.getByText('Enter whole days from 1 to 90.')).toBeVisible();
	await field.fill('14');
	await page.getByRole('button', { name: 'Save the quiet window' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Saved: the quiet window is 14 days.' })).toBeVisible();
	await page.reload();
	await expect(field).toHaveValue('14');
	await field.fill('7');
	await page.getByRole('button', { name: 'Save the quiet window' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Saved: the quiet window is 7 days.' })).toBeVisible();
});

test('the Systems page: a tag shown always', async ({ page }) => {
	await ownLook(page);
	await signIn(page, 'admin');
	await page.goto('/admin/systems');
	const tags = page.getByRole('table', { name: 'Tags' });
	await expect(tags.getByRole('columnheader', { name: /Always show/ })).toBeVisible();
	await expect(tags.getByRole('row', { name: /fireground/ }).getByRole('cell', { name: 'No', exact: true })).toBeVisible();
});
