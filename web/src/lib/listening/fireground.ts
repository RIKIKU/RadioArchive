// Fireground links (SPEC.md R101; ADR 0006, ADR 0057): /?fireground=508,
// or several, /?fireground=508&fireground=540&fireground=542, as
// PagerArchive's one Listen button opens it for a page or a job.
//
// The first MAX_FIREGROUNDS values are read and the rest ignored. Each is
// read as the server reads a fireground channel number (gate.ParseFireground):
// decimal digits only, up to 16, leading zeros not counting, and no bigger
// than a 32-bit integer. Anything else is a channel that isn't available.

/** The link's parameter. */
export const FIREGROUND_PARAM = 'fireground';

/** How many of the link's values are read; those after are ignored. */
export const MAX_FIREGROUNDS = 10;

const MAX_DIGITS = 16;
const MAX_NUMBER = 2_147_483_647;

/** A fireground channel number, or undefined if the text isn't one. */
export function parseFireground(s: string): number | undefined {
	if (s.length === 0 || s.length > MAX_DIGITS || !/^[0-9]+$/.test(s)) return undefined;
	const n = Number(s.replace(/^0+/, '') || '0');
	return n <= MAX_NUMBER ? n : undefined;
}

/**
 * What a link asks for: the distinct channel numbers among its first
 * MAX_FIREGROUNDS values, in their order, and how many distinct channels it
 * names, numbers or not, so the page can say how many aren't available
 * without naming any (R33).
 */
export function linkNumbers(values: string[]): { numbers: number[]; asked: number } {
	const numbers: number[] = [];
	const malformed = new Set<string>();
	for (const v of values.slice(0, MAX_FIREGROUNDS)) {
		const n = parseFireground(v);
		if (n === undefined) malformed.add(v);
		else if (!numbers.includes(n)) numbers.push(n);
	}
	return { numbers, asked: numbers.length + malformed.size };
}

/** The several-number lookup's address (ADR 0057). */
export const firegroundsURL = (numbers: number[]) => `/api/v1/fireground?${numbers.map((n) => `n=${n}`).join('&')}`;
