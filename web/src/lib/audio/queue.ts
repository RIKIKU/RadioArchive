// The player's queue rules (SPEC.md R43, R44; ADR 0012), on their own so
// they can be tested without a browser (tests/queue-rules.spec.ts). The
// player (player.svelte.ts) holds the calls; these say which plays next and
// which hold lets through.
//
// - One call plays at a time; the rest wait in order of start time.
// - A priority call (any talkgroup of its patch marked priority in the
//   listening set) goes before every call that isn't, in start-time order
//   among priority calls. It never cuts off the call playing: the player
//   asks only when nothing plays.
// - A call asked for again (Play again) goes first of all.
// - With hold on, only calls heard on the held talkgroup play. Calls queued
//   before hold was switched on stay queued and play once it's off (nothing
//   is dropped automatically, R43); calls that start on other talkgroups
//   while it's on aren't queued at all (A9).

/** What the rules need to know of a call. */
export interface QueuedCall {
	talkgroup: { id: number };
	/** The other talkgroups of its patch (R16). */
	patched?: number[];
	/** Its system, when the deployment has several (R15). */
	system?: { id: number };
	/** UTC, ISO 8601 with milliseconds, as call events carry it. */
	startedAt: string;
	/** Asked for again: plays before everything else. */
	first?: boolean;
}

/** A talkgroup as the listening set names it: its system only when there are several. */
export interface TalkgroupKey {
	system?: number;
	id: number;
}

/** Every talkgroup a call is heard on: its own and its patch's, in its system. */
export function talkgroupsOf(c: QueuedCall): TalkgroupKey[] {
	const system = c.system?.id;
	const ids = [c.talkgroup.id, ...(c.patched ?? []).filter((id) => id !== c.talkgroup.id)];
	return ids.map((id) => (system === undefined ? { id } : { system, id }));
}

/** Whether hold, on the talkgroup held, lets the call through. */
export function heldThrough(c: QueuedCall, held: TalkgroupKey | undefined): boolean {
	if (!held) return true;
	if (held.system !== undefined && c.system && c.system.id !== held.system) return false;
	return talkgroupsOf(c).some((t) => t.id === held.id);
}

/** Start time, then the order they came in. */
const earlier = (a: QueuedCall, b: QueuedCall) => (a.startedAt < b.startedAt ? -1 : a.startedAt > b.startedAt ? 1 : 0);

/**
 * The index of the call to play next, or -1 if none may: a call asked for
 * again, then priority calls by start time, then the rest by start time,
 * leaving out what hold holds back. Calls that started at the same moment
 * keep the order they came in.
 */
export function nextIndex<C extends QueuedCall>(queue: readonly C[], priority: (c: C) => boolean, held?: TalkgroupKey): number {
	let best = -1;
	let bestRank = 0;
	queue.forEach((c, i) => {
		if (!heldThrough(c, held)) return;
		const rank = c.first ? 2 : priority(c) ? 1 : 0;
		if (best < 0 || rank > bestRank || (rank === bestRank && earlier(c, queue[best]) < 0)) {
			best = i;
			bestRank = rank;
		}
	});
	return best;
}
