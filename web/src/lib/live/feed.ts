// The listener's live feed (SPEC.md R36, R37; ADR 0007): one WebSocket to
// /api/v1/live. It sends the listening set as a `set` message, again
// whenever the set changes, keeps the highest event ID it has seen, and
// after a dropped connection opens a new one and resumes from that ID.
// Message types it doesn't know are ignored.

import type { LiveTalkgroup } from './listening-set.ts';
import { limitOf } from '../limits.ts';

export const LIVE_PATH = '/api/v1/live';

/** A talkgroup as call events carry it (ADR 0007). */
export interface EventTalkgroup {
	id: number;
	label: string;
	name: string;
	tag?: string;
	groups?: string[];
	led_colour?: string | null;
	alert?: string | null;
}

/** A call event: call_start, transmission, call_end, call_delayed (ADR 0007). */
export interface CallEvent {
	type: 'call_start' | 'transmission' | 'call_end' | 'call_delayed';
	event_id: number;
	call_id: string;
	talkgroup: EventTalkgroup;
	patched_talkgroups?: number[];
	unit: { id: number; name: string } | null;
	started_at: string;
	system?: { id: number; label: string; name: string };
	offset_ms?: number;
	ended_at?: string;
	duration_ms?: number;
	delay_minutes?: number;
	recording?: { url: string; type: string; size: number };
}

export interface FeedHandlers {
	/** A call event, in the order the server sent it. */
	event(e: CallEvent): void;
	/** A call has been withdrawn (R31): end it, playing or queued, and forget it. */
	withdrawn(callId: string): void;
	/** One Opus frame of a live call. */
	frame(callId: string, seq: number, packet: Uint8Array): void;
	/** The socket is open and the set taken, or not. */
	connected?(on: boolean): void;
	/**
	 * A feed notice is on or off (R15, R71; ADR 0007, 0010, 0037). A new
	 * socket starts with every notice off: each welcome calls this with
	 * every kind the listener has been told is on, off. A refused socket's
	 * notice comes with the server's message (ADR 0039).
	 */
	notice?(kind: NoticeKind, active: boolean, message?: string): void;
}

/**
 * The feed notices (ADR 0007): the recorder notice (R15), which the server
 * sends, and a socket refused over a limit (R71, ADR 0037), which the feed
 * raises itself (ADR 0039): "the radio is full", or too many from here.
 */
export type NoticeKind = 'recorders' | 'radio_full' | 'too_many_live';
const NOTICES: readonly NoticeKind[] = ['recorders', 'radio_full', 'too_many_live'];

/** The binary frame's header (ADR 0007): kind, reserved, call ID, frame number. */
export const FRAME_HEADER = 24;
const KIND_OPUS = 0x01;

const hex = (b: Uint8Array) => Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');

/** Reads one binary frame: its call's ID in text form, its number and its packet. */
export function readFrame(buf: ArrayBuffer): { callId: string; seq: number; packet: Uint8Array } | undefined {
	if (buf.byteLength <= FRAME_HEADER) return undefined;
	const b = new Uint8Array(buf);
	if (b[0] !== KIND_OPUS) return undefined;
	const h = hex(b.subarray(4, 20));
	const callId = `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
	const seq = new DataView(buf).getUint32(20, true);
	return { callId, seq, packet: b.subarray(FRAME_HEADER) };
}

/** How long to wait before the n-th reconnect in a row: 0.5 s doubling to 10 s, with jitter. */
const backoff = (n: number) => Math.min(10_000, 500 * 2 ** n) * (0.75 + Math.random() / 2);

export class LiveFeed {
	#h: FeedHandlers;
	#url: string;
	#ws: WebSocket | undefined;
	#set: LiveTalkgroup[] = [];
	#lastEventId: number | undefined;
	#welcomed = false;
	#stopped = true;
	#tries = 0;
	#timer: ReturnType<typeof setTimeout> | undefined;

	constructor(h: FeedHandlers, url?: string) {
		this.#h = h;
		this.#url = url ?? `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}${LIVE_PATH}`;
	}

	/** The highest event ID seen, which a reconnect resumes from. */
	get lastEventId(): number | undefined {
		return this.#lastEventId;
	}

	/** Opens the feed, hearing set. */
	start(set: LiveTalkgroup[]) {
		this.#set = set;
		if (!this.#stopped) return;
		this.#stopped = false;
		this.#connect();
	}

	/** Changes what the feed hears: sent at once when the socket is open. */
	setTalkgroups(set: LiveTalkgroup[]) {
		this.#set = set;
		if (this.#welcomed) this.#sendSet();
	}

	/** Closes the feed for good. */
	stop() {
		this.#stopped = true;
		clearTimeout(this.#timer);
		const ws = this.#ws;
		this.#ws = undefined;
		this.#welcomed = false;
		ws?.close(1000);
		for (const k of NOTICES) if (k !== 'recorders') this.#h.notice?.(k, false);
	}

	#connect() {
		const ws = new WebSocket(this.#url);
		ws.binaryType = 'arraybuffer';
		this.#ws = ws;
		let opened = false;
		ws.onopen = () => (opened = true);
		ws.onmessage = (m) => {
			if (this.#ws !== ws) return;
			if (m.data instanceof ArrayBuffer) {
				const f = readFrame(m.data);
				if (f) this.#h.frame(f.callId, f.seq, f.packet);
				return;
			}
			this.#text(String(m.data));
		};
		ws.onclose = () => {
			if (this.#ws !== ws) return;
			this.#ws = undefined;
			this.#welcomed = false;
			this.#h.connected?.(false);
			if (this.#stopped) return;
			if (opened) {
				this.#retry(backoff(this.#tries++));
				return;
			}
			// Closed before it opened: maybe refused over a limit, which a
			// WebSocket can't say, so the feed asks (ADR 0037, 0039).
			void this.#refusal().then((l) => {
				if (this.#stopped || this.#ws) return;
				if (!l) {
					this.#retry(backoff(this.#tries++));
					return;
				}
				const kind: NoticeKind = l.error === 'radio_full' ? 'radio_full' : 'too_many_live';
				this.#h.notice?.(kind === 'radio_full' ? 'too_many_live' : 'radio_full', false);
				this.#h.notice?.(kind, true, l.message);
				this.#retry(l.retry_after * 1000);
			});
		};
	}

	#retry(ms: number) {
		clearTimeout(this.#timer);
		this.#timer = setTimeout(() => {
			if (!this.#stopped) this.#connect();
		}, ms);
	}

	/** Why the last socket was refused, if over a limit: the live path asked by a plain GET (ADR 0037). */
	async #refusal() {
		try {
			const r = await fetch(this.#url.replace(/^ws/, 'http'), { headers: { Accept: 'application/json' } });
			return await limitOf(r);
		} catch {
			return undefined;
		}
	}

	#text(text: string) {
		let m: { type?: unknown; event_id?: unknown; last_event_id?: unknown; call_id?: unknown; notice?: unknown; active?: unknown };
		try {
			m = JSON.parse(text);
		} catch {
			return;
		}
		if (typeof m.event_id === 'number' && (this.#lastEventId === undefined || m.event_id > this.#lastEventId)) this.#lastEventId = m.event_id;
		switch (m.type) {
			case 'welcome':
				// The first socket resumes from the latest event when it opened,
				// so the first event it could miss is the next one.
				if (this.#lastEventId === undefined && typeof m.last_event_id === 'number') this.#lastEventId = m.last_event_id;
				this.#welcomed = true;
				// A new socket starts with every notice off; one that's on comes next.
				for (const k of NOTICES) this.#h.notice?.(k, false);
				this.#sendSet(true);
				return;
			case 'listening':
				this.#tries = 0;
				this.#h.connected?.(true);
				return;
			case 'call_start':
			case 'transmission':
			case 'call_end':
			case 'call_delayed':
				if (typeof m.call_id === 'string') this.#h.event(m as CallEvent);
				return;
			case 'call_withdrawn':
				if (typeof m.call_id === 'string') this.#h.withdrawn(m.call_id);
				return;
			case 'notice':
				if (NOTICES.includes(m.notice as NoticeKind) && typeof m.active === 'boolean') this.#h.notice?.(m.notice as NoticeKind, m.active);
				return;
		}
	}

	#sendSet(first = false) {
		const msg: { type: 'set'; talkgroups: LiveTalkgroup[]; last_event_id?: number } = { type: 'set', talkgroups: this.#set };
		// last_event_id is read only in a socket's first set.
		if (first && this.#lastEventId !== undefined) msg.last_event_id = this.#lastEventId;
		try {
			this.#ws?.send(JSON.stringify(msg));
		} catch {
			// Closing: the reconnect sends it.
		}
	}
}
