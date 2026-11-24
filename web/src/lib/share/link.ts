// Call links (SPEC.md R45, R46, R60, R61; ADR 0017): Share copies a call's
// link, /calls/<call ID>, which opens the radio with the call's card. The
// ID is the call's own opaque one (R60), as the API gives it.

/** The path of a call's link. */
export const callPath = (id: string): string => `/calls/${encodeURIComponent(id)}`;

/** A call's link, whole, to copy. */
export const callLink = (id: string, origin: string = location.origin): string => origin + callPath(id);

/** Copies text to the clipboard; false when the browser won't. */
export async function copyText(text: string): Promise<boolean> {
	try {
		await navigator.clipboard.writeText(text);
		return true;
	} catch {
		return false;
	}
}
