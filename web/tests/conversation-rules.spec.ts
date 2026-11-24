// A shared conversation's link on its own, with no browser (SPEC.md R66;
// ADR 0015, ADR 0020): web/src/lib/share/conversation.ts, and the packed
// list read back (unpack, web/src/lib/basket/basket.ts). The page is
// conversation.spec.ts's; hidden calls and Play all are
// conversation-live.spec.ts's.
import { test, expect } from '@playwright/test';
import { MAX_ZIP_URL, ZIP_PREFIX, pack, unpack, zipURL, type ZipList } from '../src/lib/basket/basket.ts';
import { CONVERSATION_PATH, MAX_LINK, conversationPath, readConversation, shareOf } from '../src/lib/share/conversation.ts';

test.beforeEach(() => test.skip(test.info().project.name !== 'chromium', 'no browser: once is enough'));

const id = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const IN_FIRE = '6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f';
const calls = (n: number) => Array.from({ length: n }, (_, i) => id(i));

test("the link is /conversation# and ADR 0015's packing of one conversation, as the server's reference encoder writes it", () => {
	const c = { name: 'Structure fire', note: 'Pumper 1 first on scene', calls: [IN_FIRE] };
	// public.ZipURL(ZipRequest{Conversations: [{Structure fire, Pumper 1 first on scene, [IN_FIRE]}]}), ADR 0020's example.
	expect(conversationPath(c)).toBe('/conversation#AQAAAW8dLDtKWU6PnXwLGiw9Tl8BDlN0cnVjdHVyZSBmaXJlF1B1bXBlciAxIGZpcnN0IG9uIHNjZW5lAQA');
	const l: ZipList = { calls: [], conversations: [c] };
	expect(conversationPath(c)).toBe(CONVERSATION_PATH + '#' + zipURL(l).slice(ZIP_PREFIX.length));
	expect(MAX_LINK).toBe(MAX_ZIP_URL);
	expect(readConversation(conversationPath(c).slice(CONVERSATION_PATH.length))).toEqual(c);
});

test("a call costs what ADR 0015 says: about 21.3 characters for its ID and 1.3 for its place, 2.7 past 128 calls", () => {
	const len = (n: number) => conversationPath({ name: 'Structure fire', note: '', calls: calls(n) }).length;
	expect((len(101) - len(1)) / 100).toBeCloseTo((17 * 4) / 3, 1);
	expect((len(300) - len(200)) / 100).toBeCloseTo((18 * 4) / 3, 1);
});

test('at the URL budget: the most calls that fit are shared and read back; one more is too many', () => {
	const name = 'Structure fire';
	let n = 1;
	while (conversationPath({ name, note: '', calls: calls(n + 1) }).length <= MAX_LINK) n++;
	// As ADR 0015's zip URL in one conversation: 325 calls.
	expect(n).toBe(325);
	const at = shareOf({ name, note: '', calls: calls(n) });
	expect(at).toEqual({ path: conversationPath({ name, note: '', calls: calls(n) }), shortened: false });
	expect(at.path!.length).toBeLessThanOrEqual(MAX_LINK);
	expect(readConversation(at.path!.split('#')[1])?.calls).toEqual(calls(n));
	expect(shareOf({ name, note: '', calls: calls(n + 1) })).toEqual({ problem: 'too-many' });
	expect(shareOf({ name, note: 'n', calls: [] })).toEqual({ problem: 'empty' });
});

test('a note too long for the link is cut at a character, ending "…", and the link is at the budget', () => {
	// 2,000 bytes of note in two-byte characters, and 250 calls.
	const note = 'é'.repeat(1000);
	const s = shareOf({ name: 'Structure fire', note, calls: calls(250) });
	expect(s.path && s.shortened).toBe(true);
	expect(s.path!.length).toBeLessThanOrEqual(MAX_LINK);
	const back = readConversation(s.path!.split('#')[1])!;
	expect(back.note.endsWith('…')).toBe(true);
	expect(note.startsWith(back.note.slice(0, -1))).toBe(true);
	// The longest cut: one more character doesn't fit.
	const longer = conversationPath({ name: 'Structure fire', note: note.slice(0, back.note.length) + '…', calls: calls(250) });
	expect(longer.length).toBeGreaterThan(MAX_LINK);
	// The same note with 10 calls fits whole.
	expect(shareOf({ name: 'Structure fire', note, calls: calls(10) })).toMatchObject({ shortened: false });
});

test('markup, line breaks and any language in the name and note come back as the same text', () => {
	const c = { name: '<img src=x onerror="alert(1)"> Fire', note: '<b>bold</b>\n<script>alert(2)</script>\nשלום 火事 🚒', calls: [IN_FIRE, id(1)] };
	expect(readConversation(conversationPath(c))).toEqual(c);
});

test("anything Share doesn't write is no conversation: the page says it isn't available", () => {
	const good = conversationPath({ name: 'Structure fire', note: '', calls: [IN_FIRE] }).split('#')[1];
	const b64 = (b: number[] | Uint8Array) => Buffer.from(Uint8Array.from(b)).toString('base64url');
	const bytes = [...Buffer.from(good, 'base64url')];
	for (const [what, s] of [
		['empty', ''],
		['not base64url', 'not a conversation!'],
		['a stray character', good + 'A'],
		['padding', good + '='],
		['the wrong format', b64([2, ...bytes.slice(1)])],
		['trailing bytes', b64([...bytes, 0])],
		['cut short', good.slice(0, -4)],
		// A zip's list that isn't one conversation alone: a loose call, two conversations, a part.
		['a loose call', zipURL({ calls: [id(1)], conversations: [{ name: 'A', note: '', calls: [IN_FIRE] }] }).slice(ZIP_PREFIX.length)],
		['two conversations', zipURL({ calls: [], conversations: [{ name: 'A', note: '', calls: [IN_FIRE] }, { name: 'B', note: '', calls: [id(1)] }] }).slice(ZIP_PREFIX.length)],
		['a part', zipURL({ calls: [], conversations: [{ name: 'A', note: '', calls: [IN_FIRE] }] }, 1, 2).slice(ZIP_PREFIX.length)],
		['a blank name', b64(pack({ calls: [], conversations: [{ name: ' ', note: '', calls: [IN_FIRE] }] }))],
		// Calls out of the packing's order: indexes [1, 0].
		['out of order', b64([1, 0, 0, 2, ...Buffer.from(IN_FIRE.replaceAll('-', ''), 'hex'), ...Buffer.from(id(1).replaceAll('-', ''), 'hex'), 1, 1, 65, 0, 2, 1, 0])],
		// A name that isn't UTF-8.
		['not UTF-8', b64([1, 0, 0, 1, ...Buffer.from(IN_FIRE.replaceAll('-', ''), 'hex'), 1, 1, 0xff, 0, 1, 0])],
		// A varint longer than needed: 1 call written in two bytes.
		['a long varint', b64([1, 0, 0, 0x81, 0x00, ...Buffer.from(IN_FIRE.replaceAll('-', ''), 'hex'), 1, 1, 65, 0, 1, 0])],
		// More calls than a list holds.
		['501 calls', b64([1, 0, 0, 0xf5, 0x03])]
	] as const) {
		expect(readConversation(s), what).toBeUndefined();
	}
	expect(unpack(good)).toBeDefined();
});
