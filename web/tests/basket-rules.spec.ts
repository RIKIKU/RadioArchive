// The download basket on its own, with no browser (SPEC.md R64, R65, R72;
// ADR 0015, ADR 0018): web/src/lib/basket/basket.ts. The page is
// basket.spec.ts's; A19 is basket-live.spec.ts's.
import { test, expect } from '@playwright/test';
import {
	MAX_ZIP_CALLS,
	MAX_ZIP_URL,
	countdownOf,
	emptyBasket,
	listOf,
	looseOf,
	pack,
	parseBasket,
	withCall,
	withConversation,
	withDownloaded,
	withInConversation,
	withoutCall,
	zipParts,
	zipURL,
	type Basket,
	type ZipList
} from '../src/lib/basket/basket.ts';

test.beforeEach(() => test.skip(test.info().project.name !== 'chromium', 'no browser: once is enough'));

const id = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const LOOSE = '0f8e2c1a-6b3d-4e5f-9a7b-1c2d3e4f5a6b';
const IN_FIRE = '6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f';

/** The packed list read back: the format byte, part, parts and how many calls (all one byte here). */
const head = (url: string) => {
	const b = Buffer.from(url.slice('/api/v1/zip/'.length), 'base64url');
	return { version: b[0], part: b[1], parts: b[2], calls: b[3] < 128 ? b[3] : (b[3] & 0x7f) + b[4] * 128 };
};

test("the zip URL is ADR 0015's packing, as the server's reference encoder writes it", () => {
	const l: ZipList = { calls: [LOOSE], conversations: [{ name: 'Structure fire', note: '', calls: [IN_FIRE] }] };
	expect(zipURL(l)).toBe('/api/v1/zip/AQAAAg-OLBprPU5fmnscLT5PWmtvHSw7SllOj518CxosPU5fAQ5TdHJ1Y3R1cmUgZmlyZQABAQ');
	// The server's listOf: a loose call also in a conversation is in the
	// conversation, a call named twice is named once, IDs in capitals are
	// the same calls, and an empty conversation is left out.
	expect(zipURL({ calls: [LOOSE, IN_FIRE.toUpperCase(), LOOSE], conversations: [{ name: 'Structure fire', note: '', calls: [IN_FIRE, IN_FIRE] }, { name: 'Empty', note: '', calls: [] }] })).toBe(zipURL(l));
	// A call in two conversations is listed once, and named by index in each.
	const two = pack({ calls: [], conversations: [{ name: 'A', note: '', calls: [LOOSE] }, { name: 'B', note: 'n', calls: [LOOSE] }] });
	expect([...two]).toEqual([1, 0, 0, 1, ...Buffer.from(LOOSE.replaceAll('-', ''), 'hex'), 2, 1, 65, 0, 1, 0, 1, 66, 1, 110, 1, 0]);
	// Varints past 127 take two bytes.
	const many = Array.from({ length: 200 }, (_, i) => id(i));
	expect(head(zipURL({ calls: many, conversations: [] })).calls).toBe(200);
});

test('a basket too big for one URL downloads in parts, each within the URL budget and 500 calls (R65, R72)', () => {
	// 400 loose calls: a GET part holds at most 359 (ADR 0015).
	const loose = Array.from({ length: 400 }, (_, i) => id(i));
	const parts = zipParts({ calls: loose, conversations: [] });
	expect(parts.map((p) => [p.part, p.parts])).toEqual([
		[1, 2],
		[2, 2]
	]);
	expect(parts.map((p) => p.calls)).toEqual([359, 41]);
	for (const p of parts) {
		expect(p.url.length).toBeLessThanOrEqual(MAX_ZIP_URL);
		expect(head(p.url)).toMatchObject({ version: 1, part: p.part, parts: 2 });
	}
	// One that fits is the whole basket: part 0 of 0.
	const one = zipParts({ calls: loose.slice(0, 359), conversations: [] });
	expect(one).toHaveLength(1);
	expect(head(one[0].url)).toMatchObject({ part: 0, parts: 0, calls: 359 });
	expect(one[0].url.length).toBeLessThanOrEqual(MAX_ZIP_URL);
	expect(zipParts({ calls: loose.slice(0, 360), conversations: [] })).toHaveLength(2);

	// Three conversations with long notes: each kept whole, two to a part.
	const note = 'n'.repeat(2000);
	const convs = [0, 1, 2].map((c) => ({ name: `Incident ${c + 1}`, note, calls: [id(1000 + c * 2), id(1001 + c * 2)] }));
	const split = zipParts({ calls: [id(999)], conversations: convs });
	expect(split.length).toBe(2);
	expect(split.reduce((n, p) => n + p.calls, 0)).toBe(7);
	for (const p of split) expect(p.url.length).toBeLessThanOrEqual(MAX_ZIP_URL);

	// One conversation of 400 calls: split, its name and note in each part.
	const big = { name: 'Big fire', note: 'A long night', calls: Array.from({ length: 400 }, (_, i) => id(5000 + i)) };
	const halves = zipParts({ calls: [], conversations: [big] });
	expect(halves.length).toBe(2);
	expect(halves[0].calls + halves[1].calls).toBe(400);
	for (const p of halves) {
		expect(p.calls).toBeLessThanOrEqual(MAX_ZIP_CALLS);
		expect(p.url.length).toBeLessThanOrEqual(MAX_ZIP_URL);
		expect(Buffer.from(p.url.slice(12), 'base64url').includes(Buffer.from('Big fire'))).toBe(true);
	}
	expect(zipParts({ calls: [], conversations: [] })).toEqual([]);
});

const at = (b: Basket, ...ids: string[]) => ids.reduce((x, i, n) => withCall(x, i, 1000 + n), b);

test('what a download takes: the whole basket or one conversation, available calls only, and new since last time', () => {
	let b = at(emptyBasket(), id(1), id(2), id(3), id(4));
	b = withConversation(b, 'c1', 'Structure fire', 'Two trucks');
	for (const c of [id(1), id(2), id(3)]) b = withInConversation(b, 'c1', c, 2000);
	expect(looseOf(b).map((c) => c.id)).toEqual([id(4)]);
	const ok = (x: string) => x !== id(2);
	// A18's basket: 3 calls in "Structure fire" and 1 loose; one gone is left out.
	expect(listOf(b, { kind: 'basket' }, () => true)).toEqual({ calls: [id(4)], conversations: [{ name: 'Structure fire', note: 'Two trucks', calls: [id(1), id(2), id(3)] }] });
	expect(listOf(b, { kind: 'basket' }, ok).conversations[0].calls).toEqual([id(1), id(3)]);
	expect(listOf(b, { kind: 'conversation', id: 'c1' }, ok)).toEqual({ calls: [], conversations: [{ name: 'Structure fire', note: 'Two trucks', calls: [id(1), id(3)] }] });

	// Downloaded at 3000: then one more call, loose, and one more in the conversation.
	b = withDownloaded(b, { kind: 'basket' }, 3000);
	expect(listOf(b, { kind: 'basket' }, () => true, true)).toEqual({ calls: [], conversations: [] });
	b = withCall(b, id(5), 4000);
	b = withCall(b, id(6), 4000);
	b = withInConversation(b, 'c1', id(6), 4000);
	// An older call put in the conversation since is new to it, too.
	b = withInConversation(b, 'c1', id(4), 4500);
	expect(listOf(b, { kind: 'basket' }, () => true, true)).toEqual({ calls: [id(5)], conversations: [{ name: 'Structure fire', note: 'Two trucks', calls: [id(6), id(4)] }] });
	expect(listOf(b, { kind: 'conversation', id: 'c1' }, () => true, true).conversations[0].calls).toEqual([id(6), id(4)]);
	// The full re-download takes them all.
	expect(listOf(b, { kind: 'basket' }, () => true).conversations[0].calls).toHaveLength(5);
	// A conversation's own download doesn't move the basket's last time.
	const c = withDownloaded(b, { kind: 'conversation', id: 'c1' }, 5000);
	expect(c.last).toBe(3000);
	expect(listOf(c, { kind: 'conversation', id: 'c1' }, () => true, true)).toEqual({ calls: [], conversations: [] });
});

test('a call in a conversation goes by its start; a call taken out of the basket leaves its conversations', () => {
	const start: Record<string, string> = { [id(1)]: '2026-11-25T10:00:00Z', [id(2)]: '2026-11-25T09:00:00Z', [id(3)]: '2026-11-25T11:00:00Z' };
	let b = withConversation(emptyBasket(), 'c1', 'Fire', '');
	for (const c of [id(1), id(3), id(2)]) b = withInConversation(b, 'c1', c, 1, (x) => start[x]);
	expect(b.conversations[0].calls.map((c) => c.id)).toEqual([id(2), id(1), id(3)]);
	expect(b.calls.map((c) => c.id)).toEqual([id(1), id(3), id(2)]);
	b = withoutCall(b, id(1));
	expect(b.conversations[0].calls.map((c) => c.id)).toEqual([id(2), id(3)]);
	// A name is needed.
	expect(withConversation(b, 'c2', '  ', '')).toBe(b);
});

test('a kept basket is read forgivingly: what isn\'t one is left out, the rest kept', () => {
	const b = parseBasket({
		v: 1,
		calls: [{ id: id(1).toUpperCase(), added: 5 }, { id: 'nonsense', added: 1 }, { id: id(1), added: 6 }, { id: id(2) }],
		conversations: [
			{ id: 'c1', name: 'Fire', calls: [{ id: id(1), added: 7 }, { id: id(9), added: 7 }] },
			{ id: 'c1', name: 'Twice', calls: [] },
			{ id: 'c 2', name: 'Odd ID', calls: [] },
			{ id: 'c3', name: '', calls: [] }
		],
		last: -1
	});
	expect(b).toEqual({ calls: [{ id: id(1), added: 5 }], conversations: [{ id: 'c1', name: 'Fire', calls: [{ id: id(1), added: 7 }] }] });
	expect(parseBasket(null)).toEqual(emptyBasket());
	expect(parseBasket('[]')).toEqual(emptyBasket());
});

test('the countdown: days, then hours, from the moment the call details give (A19: deleted in 1 day)', () => {
	const now = Date.parse('2026-11-25T12:00:00Z');
	const in_ = (h: number) => new Date(now + h * 3_600_000).toISOString();
	expect(countdownOf(in_(24 + 15), now)).toBe('deleted in 1 day');
	expect(countdownOf(in_(72.5), now)).toBe('deleted in 3 days');
	expect(countdownOf(in_(47.9), now)).toBe('deleted in 1 day');
	expect(countdownOf(in_(23.5), now)).toBe('deleted in 24 hours');
	expect(countdownOf(in_(0.2), now)).toBe('deleted in 1 hour');
	expect(countdownOf(in_(-1), now)).toBe('deleted at the next nightly run');
	expect(countdownOf(null, now)).toBeUndefined();
});
