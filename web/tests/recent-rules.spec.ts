// The Recent calls page's filters and searches on their own, with no
// browser (SPEC.md R46, R73; ADR 0013, ADR 0016): web/src/lib/recent/search.ts.
// The page itself is recent.spec.ts's.
import { test, expect } from '@playwright/test';
import { indexOf, type Catalogue, type Talkgroup } from '../src/lib/listening/set.ts';
import { MAX_URL, chosenOf, noFilters, readFilters, requestOf, searchOf, startOf, writeFilters } from '../src/lib/recent/search.ts';

test.beforeEach(() => test.skip(test.info().project.name !== 'chromium', 'no browser: once is enough'));

const tg = (id: number, name: string, system?: number): Talkgroup => ({
	system,
	id,
	label: name.toUpperCase(),
	name,
	tag: 'dispatch',
	groups: [],
	led_colour: null,
	alert: null,
	delay_minutes: 0,
	fireground_channel: null
});
const one: Catalogue = { tags: [], groups: [], talkgroups: [tg(1001, 'Dispatch North'), tg(1002, 'Fireground 41')] };
const ix = indexOf(one);
const zone = 'Australia/Melbourne';

test('the address holds the filters as chosen, and reads back the same', () => {
	const f = readFilters(new URLSearchParams('talkgroups=1002,1001&from=2026-11-20&from_time=14:00&to=2026-11-21&to_time=06:15&unit=2001&order=oldest'));
	expect(f).toEqual({
		scope: 'chosen',
		talkgroups: [
			{ system: undefined, from: 1002, to: 1002, priority: false },
			{ system: undefined, from: 1001, to: 1001, priority: false }
		],
		from: '2026-11-20',
		to: '2026-11-21',
		fromTime: '14:00',
		toTime: '06:15',
		unit: '2001',
		order: 'oldest'
	});
	expect(writeFilters(f)).toBe('talkgroups=1002,1001&from=2026-11-20&from_time=14:00&to=2026-11-21&to_time=06:15&unit=2001&order=oldest');
	expect(writeFilters(noFilters())).toBe('');
	expect(readFilters(new URLSearchParams('talkgroups=all')).scope).toBe('all');
	// What doesn't read is left out; days the wrong way round are swapped.
	const bad = readFilters(new URLSearchParams('from=yesterday&from_time=25:00&order=random&talkgroups=DISP'));
	expect(bad).toMatchObject({ from: '', fromTime: '', order: 'newest', scope: 'chosen', talkgroups: [] });
	expect(readFilters(new URLSearchParams('from=2026-11-21&to=2026-11-20'))).toMatchObject({ from: '2026-11-20', to: '2026-11-21' });
});

test('the search: the listening set by default, every talkgroup when it is empty, the range on the deployment clock', () => {
	const set = [ix.byId.get(1001)!];
	expect(searchOf(noFilters(), ix, set, zone)).toEqual({ talkgroups: ['1001'] });
	expect(searchOf(noFilters(), ix, [], zone)).toEqual({});
	expect(searchOf({ ...noFilters(), scope: 'all' }, ix, set, zone)).toEqual({});
	// 20 Nov 14:00 to 21 Nov 06:15 in Melbourne (UTC+11), To's whole minute.
	const f = readFilters(new URLSearchParams('from=2026-11-20&from_time=14:00&to=2026-11-21&to_time=06:15&unit=1.2001&order=oldest'));
	expect(searchOf(f, ix, set, zone)).toEqual({
		talkgroups: ['1001'],
		from: '2026-11-20T03:00:00.000Z',
		to: '2026-11-20T19:16:00.000Z',
		unit: '1.2001',
		order: 'oldest'
	});
});

test('chosen talkgroups are read against the catalogue: hidden or unknown ones are dropped, and naming only those asks nothing', () => {
	const f = readFilters(new URLSearchParams('talkgroups=1003,1001-1001,4711'));
	expect(chosenOf(ix, f).map((t) => t.id)).toEqual([1001]);
	expect(searchOf(f, ix, [], zone)).toEqual({ talkgroups: ['1001'] });
	expect(searchOf(readFilters(new URLSearchParams('talkgroups=1003')), ix, [], zone)).toBeUndefined();
	// A range is tested against the catalogue, never expanded.
	expect(chosenOf(ix, readFilters(new URLSearchParams('talkgroups=1-2147483647'))).map((t) => t.id)).toEqual([1001, 1002]);
});

test('a search too long for a URL goes as a POST body with the same parameters (ADR 0016)', () => {
	const get = requestOf({ talkgroups: ['1001', '1002'], order: 'oldest' }, 'CURSOR');
	expect(get.url).toBe('/api/v1/recent-calls?talkgroups=1001%2C1002&order=oldest&cursor=CURSOR&limit=50');
	expect(get.init.method).toBeUndefined();
	// 1,000 talkgroups in several systems: past the budget.
	const many = Array.from({ length: 1000 }, (_, i) => `${65001 + (i % 3)}.${2_000_000_000 + i}`);
	const post = requestOf({ talkgroups: many, from: '2026-11-20T03:00:00.000Z' }, 'CURSOR');
	expect(`/api/v1/recent-calls?talkgroups=${encodeURIComponent(many.join(','))}`.length).toBeGreaterThan(MAX_URL);
	expect(post.url).toBe('/api/v1/recent-calls');
	expect(post.init.method).toBe('POST');
	expect(JSON.parse(String(post.init.body))).toEqual({ talkgroups: many, from: '2026-11-20T03:00:00.000Z', cursor: 'CURSOR', limit: 50 });
});

test("a call's start is shown on the deployment's clock, not the browser's", () => {
	const now = new Date('2026-11-25T00:00:00Z');
	expect(startOf('2026-11-24T18:06:07.000Z', zone, now)).toMatch(/Wed.*25 Nov.*05:06:07/);
	expect(startOf('2025-11-24T18:06:07.000Z', zone, now)).toMatch(/2025/);
});
