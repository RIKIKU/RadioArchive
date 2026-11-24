// The share link's format (ADR 0008) and the picker's search, on their
// own, with no browser: web/src/lib/listening/set.ts.
import { test, expect } from '@playwright/test';
import { decodeSet, encodeSet, indexOf, linkEntries, search, type Catalogue, type Talkgroup } from '../src/lib/listening/set.ts';

const tg = (id: number, name: string, label = '', tag = 'dispatch', system?: number): Talkgroup => ({
	system,
	id,
	label,
	name,
	tag,
	groups: [],
	led_colour: null,
	alert: null,
	delay_minutes: 0,
	fireground_channel: null
});

const one: Catalogue = {
	tags: ['dispatch', 'fireground'],
	groups: [],
	talkgroups: [tg(1001, 'Dispatch North', 'DISP N'), tg(1002, 'Fireground 41', 'FG 41', 'fireground'), ...[1010, 1011, 1012, 1013].map((i) => tg(i, `Ops ${i}`))]
};

test.beforeEach(() => test.skip(test.info().project.name !== 'chromium', 'no browser: once is enough'));

test('encoding: IDs in order, runs of three or more as ranges, p for priority', () => {
	expect(encodeSet([])).toBe('');
	expect(encodeSet([{ id: 1002, priority: true }, { id: 1001 }])).toBe('1001,1002p');
	expect(encodeSet([1013, 1010, 1012, 1011].map((id) => ({ id })))).toBe('1010-1013');
	expect(encodeSet([{ id: 1010 }, { id: 1011 }])).toBe('1010,1011');
	expect(encodeSet([{ id: 1010 }, { id: 1011, priority: true }, { id: 1012 }])).toBe('1010,1011p,1012');
	expect(encodeSet([{ system: 2, id: 1001 }, { system: 1, id: 1001, priority: true }])).toBe('1.1001p,2.1001');
});

test('decoding: round trips, and what is not an item is left out', () => {
	const set = [{ id: 1001 }, { id: 1002, priority: true }, { id: 1010 }, { id: 1011 }, { id: 1012 }, { id: 1013 }];
	const ix = indexOf(one);
	expect(linkEntries(ix, decodeSet(encodeSet(set)))).toEqual(set.map((e) => ({ system: undefined, ...e })));
	expect(decodeSet('1001,,x,-5,5-1,1002p, 1003 ,0,99999999999,1.2.3,1010-1013p')).toEqual([
		{ system: undefined, from: 1001, to: 1001, priority: false },
		{ system: undefined, from: 1002, to: 1002, priority: true },
		{ system: undefined, from: 1003, to: 1003, priority: false },
		{ system: undefined, from: 1010, to: 1013, priority: true }
	]);
	// A huge range costs no more than the catalogue: it's never expanded.
	expect(linkEntries(ix, decodeSet('1-2147483647')).length).toBe(one.talkgroups.length);
});

test('several systems: an ID with its system names that one; a bare ID names the first', () => {
	const two: Catalogue = {
		systems: [
			{ id: 1, label: 'EX', name: 'Example network' },
			{ id: 2, label: 'EY', name: 'Other network' }
		],
		tags: ['dispatch'],
		groups: [],
		talkgroups: [tg(1001, 'Dispatch North', '', 'dispatch', 1), tg(1001, 'Dispatch East', '', 'dispatch', 2)]
	};
	const ix = indexOf(two);
	expect(linkEntries(ix, decodeSet('2.1001'))).toEqual([{ system: 2, id: 1001 }]);
	expect(linkEntries(ix, decodeSet('1001p'))).toEqual([{ system: 1, id: 1001, priority: true }]);
	// One system: a system-qualified ID names the talkgroup with that ID.
	expect(linkEntries(indexOf(one), decodeSet('7.1001'))).toEqual([{ system: undefined, id: 1001 }]);
});

test('search: exact first, then starts with, then contains, by name, label, tag or ID', () => {
	const names = (q: string) => search(one.talkgroups, q).map((t) => t.name);
	expect(names('fg 41')).toEqual(['Fireground 41']);
	expect(names('1001')).toEqual(['Dispatch North']);
	expect(names('north')).toEqual(['Dispatch North']);
	expect(names('fire')).toEqual(['Fireground 41']);
	expect(names('101')).toEqual(['Ops 1010', 'Ops 1011', 'Ops 1012', 'Ops 1013']);
	expect(names('dispatch')[0]).toBe('Dispatch North');
	expect(names('')).toHaveLength(one.talkgroups.length);
	expect(names('nothing')).toEqual([]);
});
