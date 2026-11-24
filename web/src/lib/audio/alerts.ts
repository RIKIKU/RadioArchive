// Talkgroups' alert sounds (SPEC.md R22, R48; ADR 0006, 0012): `beep`,
// `chime`, `pip` and `tone`, the names ADR 0006 gives. Each is a short
// sound synthesised in the page with Web Audio, from the numbers below: the
// app ships no sound files, so there's nothing to license, fetch or cache,
// and no real radio audio. They play only in an open listener, only with
// alerts switched on in the playback settings (off by default), when a
// call starts on the talkgroup. They're not notifications.

/** The alert names ADR 0006 allows; `none` and null play nothing. */
export const ALERTS = ['beep', 'chime', 'pip', 'tone'] as const;
export type Alert = (typeof ALERTS)[number];

/** One note: a frequency (Hz), a waveform, when it starts and how long it lasts (seconds). */
interface Note {
	hz: number;
	wave: OscillatorType;
	at: number;
	len: number;
}

const sounds: Record<Alert, Note[]> = {
	// A plain short beep.
	beep: [{ hz: 880, wave: 'sine', at: 0, len: 0.15 }],
	// Two rising notes, a fifth apart, each ringing down.
	chime: [
		{ hz: 659.25, wave: 'triangle', at: 0, len: 0.35 },
		{ hz: 987.77, wave: 'triangle', at: 0.16, len: 0.45 }
	],
	// Three quick high pips.
	pip: [
		{ hz: 1320, wave: 'sine', at: 0, len: 0.05 },
		{ hz: 1320, wave: 'sine', at: 0.09, len: 0.05 },
		{ hz: 1320, wave: 'sine', at: 0.18, len: 0.05 }
	],
	// A low steady tone.
	tone: [{ hz: 523.25, wave: 'sine', at: 0, len: 0.4 }]
};

/** How loud an alert is, below a call's audio. */
const LEVEL = 0.25;

export const isAlert = (name: string | null | undefined): name is Alert => !!name && (ALERTS as readonly string[]).includes(name);

/** Plays an alert into out, now. Says whether it played (an unknown name doesn't). */
export function playAlert(ctx: BaseAudioContext, out: AudioNode, name: string | null | undefined): boolean {
	if (!isAlert(name)) return false;
	const t0 = ctx.currentTime + 0.01;
	for (const n of sounds[name]) {
		const osc = ctx.createOscillator();
		osc.type = n.wave;
		osc.frequency.value = n.hz;
		const env = ctx.createGain();
		const start = t0 + n.at;
		const end = start + n.len;
		// A quick rise, then an exponential fall, so no note clicks.
		env.gain.setValueAtTime(0.0001, start);
		env.gain.exponentialRampToValueAtTime(LEVEL, start + 0.01);
		env.gain.exponentialRampToValueAtTime(0.0001, end);
		osc.connect(env);
		env.connect(out);
		osc.start(start);
		osc.stop(end + 0.02);
		osc.onended = () => env.disconnect();
	}
	return true;
}
