// Playback settings (SPEC.md R48): whether talkgroups' alert sounds play
// (R22), off by default. Kept in the browser, and in the listener's account
// when signed in (ADR 0009). The player (#60) gives it its switch.
const KEY = 'radioarchive.playback';

export function alertsOn(): boolean {
	try {
		const kept = JSON.parse(localStorage.getItem(KEY) ?? 'null');
		return kept?.v === 1 && kept.alerts === true;
	} catch {
		return false;
	}
}

export function setAlerts(on: boolean) {
	try {
		if (on) localStorage.setItem(KEY, JSON.stringify({ v: 1, alerts: true }));
		else localStorage.removeItem(KEY);
	} catch {
		// Storage switched off: the setting lasts as long as the page.
	}
}
