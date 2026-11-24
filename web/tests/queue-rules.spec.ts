// The queue's rules on their own, with no browser (SPEC.md R43, R44; ADR
// 0012): web/src/lib/audio/queue.ts. The player and its controls, with the
// whole stack, are queue.spec.ts's.
import { test, expect } from '@playwright/test';
import { heldThrough, nextIndex, talkgroupsOf, type QueuedCall } from '../src/lib/audio/queue.ts';

test.beforeEach(() => test.skip(test.info().project.name !== 'chromium', 'no browser: once is enough'));

const call = (id: number, at: string, more: Partial<QueuedCall> = {}): QueuedCall => ({ talkgroup: { id }, startedAt: `2026-11-24T01:00:${at}Z`, ...more });
const prio = (ids: number[]) => (c: QueuedCall) => talkgroupsOf(c).some((t) => ids.includes(t.id));

test('calls play in order of start time', () => {
	const q = [call(1001, '05.000'), call(1001, '01.500'), call(1002, '03.000')];
	expect(nextIndex(q, () => false)).toBe(1);
	expect(nextIndex([], () => false)).toBe(-1);
});

test('a priority call goes first, by start time among priority calls; a call played again before all', () => {
	const q = [call(1001, '01.000'), call(1002, '04.000'), call(1002, '03.000')];
	expect(nextIndex(q, prio([1002]))).toBe(2);
	// A patch with a priority talkgroup in it is a priority call (R16).
	const patched = [call(1001, '01.000'), call(1001, '02.000', { patched: [1002] })];
	expect(nextIndex(patched, prio([1002]))).toBe(1);
	expect(nextIndex([...q, call(1001, '09.000', { first: true })], prio([1002]))).toBe(3);
});

test('calls that started at the same moment keep the order they came in', () => {
	const q = [call(1001, '01.000'), call(1002, '01.000')];
	expect(nextIndex(q, () => false)).toBe(0);
});

test("hold lets through only the held talkgroup's calls, its patches included", () => {
	const held = { id: 1001 };
	expect(heldThrough(call(1001, '01.000'), held)).toBe(true);
	expect(heldThrough(call(1002, '01.000'), held)).toBe(false);
	expect(heldThrough(call(1002, '01.000', { patched: [1001] }), held)).toBe(true);
	expect(heldThrough(call(1002, '01.000'), undefined)).toBe(true);
	// Several systems: the held one's only.
	expect(heldThrough(call(1001, '01.000', { system: { id: 2 } }), { system: 1, id: 1001 })).toBe(false);
	expect(heldThrough(call(1001, '01.000', { system: { id: 1 } }), { system: 1, id: 1001 })).toBe(true);
	// The next call is the held talkgroup's, priority or not.
	const q = [call(1002, '01.000'), call(1001, '02.000')];
	expect(nextIndex(q, prio([1002]), held)).toBe(1);
	expect(nextIndex([call(1002, '01.000')], () => false, held)).toBe(-1);
});
