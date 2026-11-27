// Avoid (SPEC.md R44; ADR 0055): one press turns the playing call's
// talkgroup off, as its chip's tap would, and skips the call. The set
// changes as for any other change, so it's saved, and saved to the account
// when signed in (ADR 0009), and the player takes that talkgroup's calls
// out of the queue (player.svelte.ts). A toast, "Avoided <talkgroup> ·
// Undo", stays AVOID_SECONDS; Undo turns the talkgroup back on, priority
// mark and all, but brings back none of the calls that left the queue.
//
// The live feed is told at once, through the set last sent to it, so it
// works on every page, the admin pages too, which don't load the catalogue
// and so never send the set themselves. A page that does sends the same set
// a moment later, which changes nothing.
import { keyOf } from '#lib/listening/set.ts';
import type { Avoided, ListeningState } from '#lib/listening/state.svelte.ts';
import { lastSent, sendListeningSet, type LiveTalkgroup } from '#lib/live/listening-set.ts';
import type { Player } from './player.svelte.ts';

/** How long the toast stays, untouched (the owner: "about 6 s"). */
export const AVOID_SECONDS = 6;

export class Avoid {
	/** The toast: the talkgroup avoided, and a count, so each press is a fresh toast. */
	toast: { name: string; n: number } | undefined = $state();

	#player: Player;
	#listening: ListeningState;
	#n = 0;
	#was: { key: LiveTalkgroup; avoided: Avoided | undefined } | undefined;

	constructor(player: Player, listening: ListeningState) {
		this.#player = player;
		this.#listening = listening;
	}

	/** Avoids the playing call's talkgroup. Does nothing while nothing plays. */
	avoid() {
		const c = this.#player.playing;
		if (!c) return;
		const key: LiveTalkgroup = c.system ? { system: c.system.id, id: c.talkgroup.id } : { id: c.talkgroup.id };
		const avoided = this.#listening.turnOff(key);
		const sent = lastSent();
		if (sent) sendListeningSet(sent.filter((t) => keyOf(t) !== keyOf(key)));
		// Turned off, it has stopped already; heard only through a patch, it
		// hasn't, and Avoid still skips it.
		if (this.#player.playing?.callId === c.callId) this.#player.skip();
		this.#was = { key, avoided };
		this.toast = { name: c.talkgroup.name, n: ++this.#n };
		window.dispatchEvent(new CustomEvent('radioarchive:avoided', { detail: { call_id: c.callId, talkgroup: key, from: avoided?.from ?? null } }));
	}

	/** Undo: the talkgroup is on again, as it was. The calls that left the queue stay gone. */
	undo() {
		const w = this.#was;
		this.dismiss();
		if (!w?.avoided) return;
		this.#listening.turnBackOn(w.avoided);
		const sent = lastSent() ?? [];
		if (!sent.some((t) => keyOf(t) === keyOf(w.key))) sendListeningSet([...sent, w.key]);
	}

	/** The toast has gone: timed out, tapped or Escape. Undo goes with it. */
	dismiss() {
		this.toast = undefined;
		this.#was = undefined;
	}
}
