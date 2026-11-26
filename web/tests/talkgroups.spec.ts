/// <reference types="node" />
// The Talkgroups page (issue #113; SPEC.md R17, R22, R24, R34, R50, R100,
// A13, A23, A25, A36; ADR 0031), signed in as the fixture's admin from the
// radio's root URL through the made-up Authentik.
//
// A23 and the filters and sorts run over 1,000 talkgroups: the admin API's
// own answer for the fixture's four, with 996 made-up talkgroups added to it
// in the browser (and a second system to the systems), so the fixture other
// tests share keeps its four. The server's half of A23 (1,000 talkgroups in
// one answer) is internal/server's (ADR 0026). The tests that change what a
// listener sees are talkgroups-live.spec.ts's; the edits here are to hidden
// 1003, and the refusals change nothing.
import { test, expect, type Page } from '@playwright/test';
import { axe, inLook, looks, ownLook, saveLook, shot, signIn, themes, widths } from './admin.ts';

interface Row {
	system_id: number;
	id: number;
	label: string;
	name: string;
	tag: string | null;
	groups: string[];
	state: 'public' | 'hidden' | 'ignored';
	unreviewed: boolean;
	delay_minutes: number;
	alert: string | null;
	led_colour: string | null;
	sort_order: number;
	fireground_channel: number | null;
	type: string;
	frequency: number | null;
	last_heard_at: string | null;
	calls_30_days: number;
}

const DAY = 24 * 60 * 60 * 1000;

/** 996 made-up talkgroups: 2000-2899 in EX, then 100 in a second system, NB (ID 2), every kind of row among them. */
function madeUp(now: number): Row[] {
	const out: Row[] = [];
	for (let i = 0; i < 996; i++) {
		const sys = i < 896 ? 1 : 2;
		const id = sys === 1 ? 2000 + i : 3000 + (i - 896);
		const north = i % 37 === 0;
		out.push({
			system_id: sys,
			id,
			label: north ? `NTH ${i}` : `TG ${id}`,
			name: north ? `North relay ${i}` : i % 11 === 0 ? '' : `Channel ${id}`,
			tag: ['dispatch', 'ops', 'fireground'][i % 3],
			groups: i % 5 === 0 ? ['Coast'] : i % 7 === 0 ? ['Coast', 'Hills'] : [],
			state: i % 50 === 0 ? 'ignored' : i % 13 === 0 ? 'hidden' : 'public',
			unreviewed: i % 41 === 0,
			delay_minutes: 0,
			alert: null,
			led_colour: null,
			sort_order: 0,
			fireground_channel: null,
			type: '',
			frequency: null,
			last_heard_at: i % 9 === 0 ? null : new Date(now - (i % 60) * DAY - i * 1000).toISOString(),
			calls_30_days: (i * 7919) % 500
		});
	}
	return out;
}

/** The admin API's answers with the made-up talkgroups and NB in them; resolves with every row. */
async function thousand(page: Page): Promise<Row[]> {
	const now = Date.now();
	let all: Row[] = [];
	await page.route('**/api/v1/admin/talkgroups', async (route) => {
		const r = await route.fetch();
		const body = (await r.json()) as { talkgroups: Row[] };
		body.talkgroups = [...body.talkgroups, ...madeUp(now)].sort((a, b) => a.system_id - b.system_id || a.id - b.id);
		all = body.talkgroups;
		await route.fulfill({ response: r, json: body });
	});
	await page.route('**/api/v1/admin/systems', async (route) => {
		const r = await route.fetch();
		const body = (await r.json()) as object[];
		await route.fulfill({ response: r, json: [...body, { id: 2, label: 'NB', name: 'Neighbouring network', auto_create: true, alert: null, led_colour: null, sort_order: 0 }] });
	});
	await signIn(page, 'admin');
	const t0 = Date.now();
	await page.goto('/admin/talkgroups');
	await expect(count(page)).toHaveText('Showing 1,000 of 1,000 talkgroups.', { timeout: 30_000 });
	await expect(rowsOf(page)).toHaveCount(100);
	console.log(`A23: the page opened with its 1,000 rows, the first 100 drawn, in ${Date.now() - t0} ms`);
	expect(all).toHaveLength(1000);
	return all;
}

const rowsOf = (page: Page) => page.locator('main table tbody tr');
const tgOf = (page: Page) => page.locator('main table tbody th');
const count = (page: Page) => page.locator('p.count');
const row = (page: Page, id: number) => rowsOf(page).filter({ has: page.locator('th', { hasText: new RegExp(`^\\s*${id}\\s*$`) }) });
const header = (page: Page, name: string) => page.locator('main table thead').getByRole('button', { name, exact: true });

test('A23: every one of 1,000 talkgroups in one request, and "north" filters the table as it is typed with no further request', async ({ page }) => {
	const asked: string[] = [];
	page.on('request', (r) => {
		if (new URL(r.url()).pathname.startsWith('/api/v1/admin/talkgroups')) asked.push(r.url());
	});
	const all = await thousand(page);
	// One request, no paging: no parameters.
	expect(asked).toEqual([new URL('/api/v1/admin/talkgroups', page.url()).href]);
	await expect(count(page)).toHaveText('Showing 1,000 of 1,000 talkgroups.');

	const after: string[] = [];
	page.on('request', (r) => after.push(r.url()));
	const search = page.getByRole('searchbox', { name: 'Search' });
	await search.click();
	const word = 'north';
	const took: number[] = [];
	for (let i = 1; i <= word.length; i++) {
		const q = word.slice(0, i);
		const want = all.filter((t) => String(t.id).includes(q) || t.label.toLowerCase().includes(q) || t.name.toLowerCase().includes(q));
		const t0 = Date.now();
		await search.press(word[i - 1]);
		await expect(count(page)).toHaveText(`Showing ${want.length.toLocaleString('en-AU')} of 1,000 talkgroups.`);
		took.push(Date.now() - t0);
		await expect(rowsOf(page)).toHaveCount(Math.min(want.length, 100));
	}
	console.log(`A23: each letter of "north" to the table filtered, in ms: ${took.join(', ')}`);
	// Dispatch North (1001) and the made-up north relays; nothing asked of the server meanwhile.
	await expect(row(page, 1001)).toContainText('Dispatch North');
	await expect(row(page, 1001)).toContainText('DISP N');
	expect(await rowsOf(page).count()).toBe(1 + all.filter((t) => t.name.startsWith('North relay')).length);
	// Every row is there to draw: the table draws a hundred, and the rest when asked.
	await search.fill('');
	await expect(page.locator('.more-rows p')).toHaveText('The first 100 of 1,000 are drawn; the search, filters and sorting cover them all.');
	await page.getByRole('button', { name: 'Show 100 more' }).click();
	await expect(rowsOf(page)).toHaveCount(200);
	await page.getByRole('button', { name: 'Show all 1,000' }).click();
	await expect(rowsOf(page)).toHaveCount(1000, { timeout: 30_000 });
	await expect(page.locator('.more-rows')).toHaveCount(0);
	expect(after).toEqual([]);
});

test('each filter and each column sorts, over 1,000 talkgroups', async ({ page }) => {
	test.setTimeout(120_000);
	const all = await thousand(page);
	const now = Date.now();
	const expectRows = async (want: Row[]) => {
		await expect(count(page)).toHaveText(`Showing ${want.length.toLocaleString('en-AU')} of 1,000 talkgroups.`);
		await expect(rowsOf(page)).toHaveCount(Math.min(want.length, 100));
	};
	const clear = () => page.getByRole('button', { name: 'Clear search and filters' }).click();

	await page.getByRole('combobox', { name: 'System' }).selectOption({ label: 'NB' });
	await expectRows(all.filter((t) => t.system_id === 2));
	await clear();
	await page.getByRole('combobox', { name: 'Group' }).selectOption('Hills');
	await expectRows(all.filter((t) => t.groups.includes('Hills')));
	await page.getByRole('combobox', { name: 'Group' }).selectOption('North');
	await expectRows(all.filter((t) => t.groups.includes('North')));
	await clear();
	await page.getByRole('combobox', { name: 'Tag' }).selectOption('ops');
	await expectRows(all.filter((t) => t.tag === 'ops'));
	await clear();
	await page.getByRole('combobox', { name: 'State' }).selectOption('hidden');
	await expectRows(all.filter((t) => t.state === 'hidden'));
	await page.getByRole('combobox', { name: 'State' }).selectOption('ignored');
	await expectRows(all.filter((t) => t.state === 'ignored'));
	await clear();
	await page.getByRole('checkbox', { name: 'Unreviewed only' }).check();
	await expectRows(all.filter((t) => t.unreviewed));
	await clear();
	// Never heard is "not heard in N days" whatever N is.
	const quiet = page.getByRole('textbox', { name: 'Not heard in (days)' });
	await quiet.fill('30');
	await expectRows(all.filter((t) => !t.last_heard_at || now - new Date(t.last_heard_at).getTime() >= 30 * DAY));
	await quiet.fill('thirty');
	await expect(quiet).toHaveAttribute('aria-invalid', 'true');
	await expect(page.locator('#f-quiet-error')).toHaveText('Enter whole days, such as 30.');
	await expectRows(all);
	await clear();
	// Filters together: EX's hidden fireground talkgroups.
	await page.getByRole('combobox', { name: 'System' }).selectOption({ label: 'EX' });
	await page.getByRole('combobox', { name: 'Tag' }).selectOption('fireground');
	await page.getByRole('combobox', { name: 'State' }).selectOption('hidden');
	await expectRows(all.filter((t) => t.system_id === 1 && t.tag === 'fireground' && t.state === 'hidden'));
	// The unreviewed are a click away.
	await clear();
	await page.getByRole('button', { name: 'Show them' }).click();
	await expect(page.getByRole('checkbox', { name: 'Unreviewed only' })).toBeChecked();
	await expectRows(all.filter((t) => t.unreviewed));
	await clear();

	// Each column: the first rows as ArchiveUx's order has them (collator,
	// numbers as numbers, empty last either way, ties as listed).
	const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });
	const order = (value: (t: Row) => string | number | null, dir: 1 | -1) =>
		all
			.map((t, i) => ({ t, i, v: value(t) }))
			.sort((p, q) => {
				const [ep, eq] = [p.v === null || p.v === '', q.v === null || q.v === ''];
				if (ep || eq) return ep === eq ? p.i - q.i : ep ? 1 : -1;
				const c = typeof p.v === 'number' && typeof q.v === 'number' ? p.v - q.v : collator.compare(String(p.v), String(q.v));
				return dir * c || p.i - q.i;
			})
			.map((x) => String(x.t.id));
	const systemLabel = (t: Row) => (t.system_id === 1 ? 'EX' : 'NB');
	const states = ['public', 'hidden', 'ignored'];
	const columns: { name: string; value: (t: Row) => string | number | null; first: 1 | -1 }[] = [
		{ name: 'TG', value: (t) => t.id, first: 1 },
		{ name: 'System', value: systemLabel, first: 1 },
		{ name: 'Label', value: (t) => t.label, first: 1 },
		{ name: 'Name', value: (t) => t.name, first: 1 },
		{ name: 'Tag', value: (t) => t.tag, first: 1 },
		{ name: 'Groups', value: (t) => t.groups.join(', '), first: 1 },
		{ name: 'State', value: (t) => states.indexOf(t.state) * 2 + (t.unreviewed ? 1 : 0), first: 1 },
		{ name: 'Last heard', value: (t) => (t.last_heard_at ? new Date(t.last_heard_at).getTime() : null), first: -1 },
		{ name: 'Calls (30 days)', value: (t) => t.calls_30_days, first: -1 }
	];
	for (const c of columns) {
		for (const dir of [c.first, -c.first as 1 | -1]) {
			await header(page, c.name).click();
			const want = order(c.value, dir);
			await expect(page.locator('main table thead th[aria-sort]')).toHaveAttribute('aria-sort', dir === 1 ? 'ascending' : 'descending');
			await expect(tgOf(page).first()).toHaveText(want[0]);
			const drawn = await tgOf(page).evaluateAll((els) => els.map((e) => e.textContent?.trim()));
			// Sorted over all 1,000 before the first hundred are drawn.
			expect([...drawn.slice(0, 5), drawn[99]], `${c.name}, ${dir === 1 ? 'ascending' : 'descending'}`).toEqual([...want.slice(0, 5), want[99]]);
		}
	}
	// All of them drawn, in the last sort's order (Calls, ascending) to the last.
	await page.getByRole('button', { name: 'Show all 1,000' }).click();
	await expect(rowsOf(page)).toHaveCount(1000, { timeout: 30_000 });
	const want = order((t) => t.calls_30_days, 1);
	expect(await tgOf(page).evaluateAll((els) => els.map((e) => e.textContent?.trim()))).toEqual(want);
});

test('editing 1001: a problem under its field, then A25: fireground channel number 41 is refused under the field, naming Fireground 41', async ({ page }) => {
	await signIn(page, 'admin');
	await page.goto('/admin/talkgroups');
	const edit = page.getByRole('button', { name: 'Edit 1001 Dispatch North' });
	await edit.click();
	const editor = page.getByRole('region', { name: 'Edit 1001 Dispatch North' });
	await expect(editor.getByRole('heading', { level: 2 })).toBeFocused();
	await expect(row(page, 1001)).toHaveAttribute('data-open', 'edit');
	// A text box with the tags as suggestions (a datalist): a combobox.
	const tag = editor.getByRole('combobox', { name: 'Tag required' });
	await expect(tag).toHaveValue('dispatch');
	await expect(tag).toHaveAttribute('required', '');
	await expect(editor.getByRole('textbox', { name: 'Label' })).toHaveValue('DISP N');
	await expect(editor.getByRole('textbox', { name: 'Groups' })).toHaveValue('North');
	await expect(editor.getByRole('combobox', { name: 'Alert' })).toHaveValue('');
	await expect(editor.getByRole('combobox', { name: 'Alert' }).locator('option').first()).toHaveText('Taken from elsewhere: none set');

	// A problem the page finds: under its field, the focus on it.
	await tag.fill('');
	await editor.getByRole('button', { name: 'Save' }).click();
	await expect(tag).toHaveAttribute('aria-invalid', 'true');
	await expect(tag).toBeFocused();
	await expect(editor.locator('#tg-tag-error')).toHaveText('Give it a tag, such as dispatch or fireground.');
	await expect(editor.locator('p.error')).toHaveText('1 field needs attention');
	await tag.fill('dispatch');

	// A25: what the server refuses, under its field, naming the talkgroup that has 41.
	const fg = editor.getByRole('textbox', { name: 'Fireground channel number' });
	await fg.fill('41');
	await editor.getByRole('button', { name: 'Save' }).click();
	await expect(fg).toHaveAttribute('aria-invalid', 'true');
	await expect(fg).toBeFocused();
	await expect(editor.locator('#tg-fireground_channel-error')).toHaveText('Fireground channel 41 is already talkgroup 1002 "Fireground 41" in system EX.');
	await expect(tag).not.toHaveAttribute('aria-invalid', /./);
	expect(((await (await page.request.get('/api/v1/admin/systems/1/talkgroups/1001')).json()) as Row).fireground_channel).toBeNull();

	// Cancel: nothing changed, the focus back on the row's Edit.
	await editor.getByRole('button', { name: 'Cancel' }).click();
	await expect(editor).toHaveCount(0);
	await expect(edit).toBeFocused();
	await expect(page.locator('p.status')).toHaveText('Nothing changed on 1001 Dispatch North.');
	await expect(row(page, 1001)).not.toHaveAttribute('data-open', /./);
});

test("editing hidden 1003: type and frequency saved, and an alert taken from its tag, set and set back to the tag's", async ({ page }) => {
	await signIn(page, 'admin');
	const api = page.request;
	const tags = (await (await api.get('/api/v1/admin/tags')).json()) as { id: number; name: string }[];
	const admin = tags.find((t) => t.name === 'admin')!;
	// The tag only 1003 has gets a chime, so 1003 takes it (ADR 0006).
	expect((await api.patch(`/api/v1/admin/tags/${admin.id}`, { data: { alert: 'chime' } })).status()).toBe(200);
	try {
		await page.goto('/admin/talkgroups');
		await page.getByRole('button', { name: 'Edit 1003 Admin' }).click();
		const editor = page.getByRole('region', { name: 'Edit 1003 Admin' });
		const alert = editor.getByRole('combobox', { name: 'Alert' });
		await expect(alert.locator('option').first()).toHaveText('Taken from its tag admin: chime');
		await editor.getByRole('textbox', { name: 'Type' }).fill('D');
		await editor.getByRole('textbox', { name: 'Frequency (MHz)' }).fill('851.0125');
		await alert.selectOption('beep');
		await editor.getByRole('button', { name: 'Save' }).click();
		await expect(page.locator('p.status')).toHaveText('Saved 1003 Admin.');
		await expect(page.getByRole('button', { name: 'Edit 1003 Admin' })).toBeFocused();
		let tg = (await (await api.get('/api/v1/admin/systems/1/talkgroups/1003')).json()) as Row;
		expect([tg.type, tg.frequency, tg.alert, tg.state]).toEqual(['D', 851012500, 'beep', 'hidden']);

		// Open again: as saved; the alert set back to the tag's.
		await page.getByRole('button', { name: 'Edit 1003 Admin' }).click();
		await expect(editor.getByRole('textbox', { name: 'Frequency (MHz)' })).toHaveValue('851.0125');
		await expect(alert).toHaveValue('beep');
		await alert.selectOption({ label: 'Taken from its tag admin: chime' });
		await editor.getByRole('button', { name: 'Save' }).click();
		await expect(page.locator('p.status')).toHaveText('Saved 1003 Admin.');
		tg = (await (await api.get('/api/v1/admin/systems/1/talkgroups/1003')).json()) as Row;
		expect(tg.alert).toBeNull();
		// Saving unchanged sends nothing.
		await page.getByRole('button', { name: 'Edit 1003 Admin' }).click();
		await editor.getByRole('button', { name: 'Save' }).click();
		await expect(page.locator('p.status')).toHaveText('Nothing to change on 1003 Admin.');
	} finally {
		await api.patch(`/api/v1/admin/tags/${admin.id}`, { data: { alert: null } });
		await api.patch('/api/v1/admin/systems/1/talkgroups/1003', { data: { type: '', frequency: null, alert: null } });
	}
});

test('ignoring and making public ask first, in a dialog; cancelled, nothing changes', async ({ page }) => {
	await signIn(page, 'admin');
	await page.goto('/admin/talkgroups');
	const more = page.getByRole('button', { name: 'More actions: 1002 Fireground 41' });
	await more.click();
	await page.getByRole('button', { name: 'Ignore…' }).click();
	const ask = page.getByRole('dialog', { name: 'Ignore 1002 Fireground 41?' });
	await expect(ask).toBeVisible();
	await expect(ask.getByRole('button', { name: 'Cancel' })).toBeFocused();
	await expect(ask).toContainText("its calls are refused, not recorded, and can't be got back");
	await ask.getByRole('button', { name: 'Cancel' }).click();
	await expect(ask).toHaveCount(0);
	await expect(more).toBeFocused();

	await page.getByRole('button', { name: 'More actions: 1003 Admin' }).click();
	await page.getByRole('button', { name: 'Make public…' }).click();
	const pub = page.getByRole('dialog', { name: 'Make 1003 Admin public?' });
	await expect(pub).toContainText('can be heard by anyone at once');
	await page.keyboard.press('Escape');
	await expect(pub).toHaveCount(0);
	for (const id of [1002, 1003]) {
		const tg = (await (await page.request.get(`/api/v1/admin/systems/1/talkgroups/${id}`)).json()) as Row;
		expect(tg.state).toBe(id === 1002 ? 'public' : 'hidden');
	}
	await expect(row(page, 1003)).toContainText('Hidden');
});

test("A13, the browser's half: an admin plays a 1003 recording from its row, in the page's own player; the Listen page, signed in as that admin, has no 1003", async ({ page }) => {
	await page.addInitScript(() => {
		const w = window as unknown as { played: object[] };
		w.played = [];
		addEventListener('radioarchive:played', (e) => w.played.push((e as CustomEvent).detail));
	});
	await signIn(page, 'admin');
	await page.goto('/admin/talkgroups');
	await expect(row(page, 1003)).toContainText('Hidden');
	await page.getByRole('button', { name: 'More actions: 1003 Admin' }).click();
	await page.getByRole('button', { name: 'Recordings' }).click();
	const panel = page.getByRole('region', { name: 'Recordings: 1003 Admin' });
	await expect(panel.getByRole('heading', { level: 2 })).toBeFocused();
	await expect(row(page, 1003)).toHaveAttribute('data-open', 'calls');
	const calls = panel.getByRole('list', { name: 'Calls on 1003 Admin' }).getByRole('listitem');
	await expect(calls.first()).toBeVisible();
	expect(await calls.count()).toBeGreaterThanOrEqual(3);
	await expect(calls.first()).toContainText('Hidden');
	const play = calls.first().getByRole('button', { name: /^Play the call of / });
	await play.click();
	await expect(panel.getByText(/^Playing the call of .*: 0:0\d$/)).toBeVisible();
	await expect(calls.first().getByRole('button', { name: /^Stop the call of / })).toHaveAttribute('aria-pressed', 'true');
	await shot(page, 'hidden-playing-1280');
	// It played (the fixture's recordings are silence), and not through the dock.
	await expect.poll(async () => page.evaluate(() => (window as unknown as { played: { frames: number; replay: boolean }[] }).played.length), { timeout: 20_000 }).toBe(1);
	const [played] = await page.evaluate(() => (window as unknown as { played: { frames: number; source: string }[] }).played);
	expect(played.frames).toBeGreaterThan(100);
	expect(played.source).toBe('recording');
	await expect(page.getByRole('region', { name: 'Now playing' }).locator('.tg')).toHaveCount(0);

	// The listener, signed in as this admin: no 1003 anywhere.
	await page.goto('/');
	await expect(page.getByRole('heading', { level: 1, name: 'Listen' })).toBeVisible();
	// Group North holds 1001 and 1003; the listener's North has 1001 only.
	await expect(page.getByRole('list', { name: 'Agencies' }).getByRole('button', { name: /^North/ })).toHaveText(/North\s*1 off/);
	await page.getByLabel('Search talkgroups').fill('north');
	await expect(page.getByRole('list', { name: 'Search results' }).getByRole('listitem')).toHaveText([/Dispatch North/]);
	await expect(page.locator('main')).not.toContainText('1003');
	await expect(page.locator('main')).not.toContainText('ADMIN');
	const catalogue = await (await page.request.get('/api/v1/catalogue')).text();
	expect(catalogue).not.toContain('1003');
	expect(catalogue).not.toContain('Admin');
});

test('signed out and as a listener the Talkgroups page refuses, as the admin API does', async ({ page }) => {
	await page.goto('/admin/talkgroups');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('This page is for admins');
	await expect(page.locator('main')).not.toContainText('Dispatch');
	expect((await page.request.get('/api/v1/admin/talkgroups')).status()).toBe(401);
	await signIn(page, 'listener');
	await page.goto('/admin/talkgroups');
	await expect(page.locator('main')).toHaveText('This page is for admins');
	expect((await page.request.get('/api/v1/admin/talkgroups')).status()).toBe(403);
});

for (const look of looks)
	for (const theme of themes)
		for (const width of widths)
			test(`axe: the Talkgroups page with a row open for editing and a problem, its recordings, and asking, ${look}, ${theme}, ${width}px`, async ({ page, context, baseURL }) => {
				test.setTimeout(90_000);
				await saveLook(context, baseURL!, look, theme);
				await ownLook(page);
				await signIn(page, 'admin');
				await page.setViewportSize({ width, height: 900 });
				const tag = `${look}-${theme}-${width}`;
				await page.goto('/admin/talkgroups');
				await expect(row(page, 1001)).toBeVisible();
				await inLook(page, look, theme);
				await axe(page, width);
				await shot(page, `table-${tag}`);

				// A row open for editing, with the server's refusal under its field.
				await page.getByRole('button', { name: 'Edit 1001 Dispatch North' }).click();
				const editor = page.getByRole('region', { name: 'Edit 1001 Dispatch North' });
				await editor.getByRole('textbox', { name: 'Fireground channel number' }).fill('41');
				await editor.getByRole('textbox', { name: 'Delay (minutes) required' }).fill('');
				await editor.getByRole('button', { name: 'Save' }).click();
				await expect(editor.locator('#tg-delay_minutes-error')).toBeVisible();
				await inLook(page, look, theme);
				await axe(page, width);
				await editor.getByRole('textbox', { name: 'Delay (minutes) required' }).fill('0');
				await editor.getByRole('button', { name: 'Save' }).click();
				await expect(editor.locator('#tg-fireground_channel-error')).toContainText('Fireground 41');
				await inLook(page, look, theme);
				await axe(page, width);
				await shot(page, `edit-${tag}`);
				await editor.getByRole('button', { name: 'Cancel' }).click();

				// Hidden 1003's recordings, one playing.
				await page.getByRole('button', { name: 'More actions: 1003 Admin' }).click();
				await page.getByRole('button', { name: 'Recordings' }).click();
				const panel = page.getByRole('region', { name: 'Recordings: 1003 Admin' });
				await panel.getByRole('button', { name: /^Play the call of / }).first().click();
				await expect(panel.getByText(/^Playing the call of /)).toBeVisible();
				await inLook(page, look, theme);
				await axe(page, width);
				await shot(page, `recordings-${tag}`);
				await panel.getByRole('button', { name: 'Close' }).click();

				// The row's More actions open, then asking in a dialog.
				await page.getByRole('button', { name: 'More actions: 1002 Fireground 41' }).click();
				await inLook(page, look, theme);
				await axe(page, width);
				await page.getByRole('button', { name: 'Ignore…' }).click();
				await expect(page.getByRole('dialog', { name: 'Ignore 1002 Fireground 41?' })).toBeVisible();
				await inLook(page, look, theme);
				await axe(page, width);
				await shot(page, `ask-${tag}`);
				await page.getByRole('dialog').getByRole('button', { name: 'Cancel' }).click();
			});
