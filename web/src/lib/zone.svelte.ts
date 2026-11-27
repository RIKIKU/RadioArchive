// The deployment's time zone (SPEC.md section 13), as the catalogue gives
// it, for the listener's pages: the now-playing display shows each call's
// start time in it (ADR 0051). The Listen page tells it when its catalogue
// loads; any other page asks for it once, the first time a time is shown.
// UTC until it's known, as the admin pages fall back to.
import { deploymentZone } from '#lib/admin/api.ts';

export const zone = $state({ name: 'UTC', known: false });

let asking = false;

/** The catalogue's time_zone, from whoever loaded it. */
export function knowZone(name: string | undefined) {
	zone.name = name || 'UTC';
	zone.known = true;
}

/** Asks the catalogue for the zone once, unless it's known or being asked. */
export function needZone() {
	if (zone.known || asking) return;
	asking = true;
	void deploymentZone().then((z) => {
		if (!zone.known) knowZone(z);
	});
}
