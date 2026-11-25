// Heavy-use limits as the listener meets them (SPEC.md R71-R73, R78; ADRs
// 0036-0039). The server refuses a request over a limit with 429 (or 503
// when the whole radio is at a cap), Retry-After, and a body naming the
// limit and when to try again. The page shows that message where the
// request was made, in place of its own "Couldn't load…".

/** A refusal's body (R78), with the wait in seconds. */
export interface Limited {
	error: string;
	message: string;
	retry_after: number;
}

/** The refusals' codes (ADRs 0036, 0037, 0038). */
export const LIMIT_CODES: readonly string[] = [
	'too_many_requests',
	'too_many_plays',
	'too_many_live',
	'radio_full',
	'too_many_downloads',
	'downloads_busy'
];

/** The refusal r is, if it's a limit's: undefined for any other answer. It reads r's body. */
export async function limitOf(r: Response): Promise<Limited | undefined> {
	if (r.status !== 429 && r.status !== 503) return undefined;
	const body = await r.json().catch(() => undefined);
	if (!body || typeof body.message !== 'string' || !LIMIT_CODES.includes(body.error)) return undefined;
	const wait = Number(body.retry_after ?? r.headers.get('Retry-After'));
	return { error: body.error, message: body.message, retry_after: Number.isFinite(wait) && wait > 0 ? wait : 30 };
}

/** An answer refused over a limit, thrown by refusedBy. */
export class LimitError extends Error {
	constructor(readonly limited: Limited) {
		super(limited.message);
	}
}

/** The error to throw for an answer that isn't ok: a LimitError for a limit's refusal, else one naming the status. */
export async function refusedBy(r: Response): Promise<Error> {
	const l = await limitOf(r);
	return l ? new LimitError(l) : new Error(String(r.status));
}

/** The message to show for err when it's a limit's refusal, else undefined (the page's own words). */
export const limitText = (err: unknown): string | undefined => (err instanceof LimitError ? err.message : undefined);

/**
 * Asks whether a download would be refused now (ADR 0038): its first byte,
 * which the server answers as it would the download. undefined: go ahead,
 * the network failing included, which the download itself then shows.
 */
export async function downloadRefusal(url: string): Promise<Limited | undefined> {
	try {
		const r = await fetch(url, { headers: { Range: 'bytes=0-0' } });
		const l = await limitOf(r);
		if (!l) await r.body?.cancel().catch(() => undefined);
		return l;
	} catch {
		return undefined;
	}
}

/** A live refusal's message with its retry said as the feed does it, trying again by itself: "… Trying again…". */
export const tryingAgain = (message: string): string => message.replace(/\s*Try again in [^.]*\.\s*$/, '') + ' Trying again…';
