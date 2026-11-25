<script lang="ts">
	// The Recorders page (SPEC.md R10, R12, R13, R14, A2, A27; ADR 0003,
	// 0010, 0027), an admin page:
	//
	// - each active recorder's state, from GET /admin/recorders: connected
	//   or not and since when, last connected, last call, its client's
	//   version and whether it's out of date, and each system's decode rate;
	//   a warning badge, with the server's words for each signal tripped
	//   (disconnected, deaf, quiet), shown as soon as the server says so;
	// - its quiet threshold, the one thing set here (PATCH, ADR 0010);
	// - calls rejected from a recorder and shortName not attached to a
	//   system, in R12's words (GET /admin/rejections);
	// - two recorders' overlapping calls on one talkgroup in the last 24 h,
	//   in R13's words (GET /admin/overlaps).
	//
	// Keys never appear (R10): a recorder's key is the server shell's alone.
	// The page asks again every few seconds, so a recorder that drops off or
	// goes deaf shows within moments, and clears when it's back.
	import { onDestroy, onMount, tick } from 'svelte';
	import { StateBadge } from 'archiveux/components';
	import AdminPage from '#lib/admin/AdminPage.svelte';
	import { admin, agoOf, deploymentZone, whenOf, type Access, type AdminError } from '#lib/admin/api.ts';

	const POLL_MS = 5000;
	const MAX_QUIET = 10_080;

	interface RecorderSystem {
		short_name: string;
		system_id: number | null;
		system_label: string | null;
		system_type: string | null;
		decode_rate: number | null;
		deaf: boolean;
	}
	interface Recorder {
		name: string;
		connected: boolean;
		connected_since: string | null;
		disconnected_since: string | null;
		last_connected_at: string | null;
		last_call_at: string | null;
		client_version: string | null;
		client_out_of_date: boolean;
		quiet_after_minutes: number | null;
		systems: RecorderSystem[];
		warning: boolean;
		warnings: string[];
	}
	interface Rejections {
		unattached: { recorder: string; short_name: string; calls: number; last_at: string | null; message: string }[];
	}
	interface Overlaps {
		overlaps: { system_id: number; system_label: string; talkgroup: number; recorders: [string, string]; calls: number; message: string }[];
	}

	let access: Access = $state('checking');
	let recorders: Recorder[] = $state([]);
	let rejected: Rejections['unattached'] = $state([]);
	let overlaps: Overlaps['overlaps'] = $state([]);
	let timeZone = $state('UTC');
	let now = $state(Date.now());

	/** The quiet threshold's field per recorder, as typed; its problem; whether it saved. */
	let typed: Record<string, string> = $state({});
	let problem: Record<string, string> = $state({});
	let saved: Record<string, string> = $state({});
	let saving: Record<string, boolean> = $state({});

	async function load() {
		const [r, j, o] = await Promise.all([
			admin<Recorder[]>('GET', '/recorders'),
			admin<Rejections>('GET', '/rejections'),
			admin<Overlaps>('GET', '/overlaps')
		]);
		now = Date.now();
		access = r.access;
		if (r.access !== 'open' || !r.body) return;
		for (const rec of r.body) if (!(rec.name in typed)) typed[rec.name] = rec.quiet_after_minutes === null ? '' : String(rec.quiet_after_minutes);
		recorders = r.body;
		if (j.access === 'open' && j.body) rejected = j.body.unattached;
		if (o.access === 'open' && o.body) overlaps = o.body.overlaps;
	}

	let timer: ReturnType<typeof setInterval> | undefined;
	onMount(() => {
		void deploymentZone().then((z) => (timeZone = z));
		void load();
		timer = setInterval(() => void load(), POLL_MS);
	});
	onDestroy(() => clearInterval(timer));

	const when = (iso: string | null, never: string) => (iso ? `${whenOf(iso, timeZone)} (${agoOf(iso, now)})` : never);
	const rate = (s: RecorderSystem) => (s.decode_rate === null ? 'No decode rate reported yet' : `${s.decode_rate.toFixed(1)} messages a second`);
	const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;
	const fieldID = (i: number) => `quiet-${i}`;

	/** Saves a recorder's quiet threshold: whole minutes, 1 to a week, or empty for off. */
	async function saveQuiet(e: SubmitEvent, rec: Recorder, i: number) {
		e.preventDefault();
		const raw = (typed[rec.name] ?? '').trim();
		saved[rec.name] = '';
		let minutes: number | null = null;
		if (raw !== '') {
			minutes = Number(raw);
			if (!/^\d+$/.test(raw) || minutes < 1 || minutes > MAX_QUIET) {
				problem[rec.name] = `Enter whole minutes from 1 to ${MAX_QUIET.toLocaleString('en-AU')}, or leave it empty for off.`;
				await tick();
				document.getElementById(fieldID(i))?.focus();
				return;
			}
		}
		problem[rec.name] = '';
		saving[rec.name] = true;
		const a = await admin<Recorder | AdminError>('PATCH', `/recorders/${encodeURIComponent(rec.name)}`, { quiet_after_minutes: minutes });
		saving[rec.name] = false;
		if (a.access !== 'open') {
			if (a.access === 'sign-in' || a.access === 'not-admin') access = a.access;
			else if (a.status === 400) problem[rec.name] = (a.body as AdminError | undefined)?.message ?? "That isn't a number of minutes.";
			else saved[rec.name] = "Couldn't save just now. Try again in a minute.";
			if (problem[rec.name]) {
				await tick();
				document.getElementById(fieldID(i))?.focus();
			}
			return;
		}
		const r = a.body as Recorder;
		recorders = recorders.map((x) => (x.name === r.name ? r : x));
		typed[rec.name] = r.quiet_after_minutes === null ? '' : String(r.quiet_after_minutes);
		saved[rec.name] = r.quiet_after_minutes === null ? 'Saved: the quiet signal is off.' : `Saved: quiet after ${plural(r.quiet_after_minutes, 'minute')} with no call.`;
	}
</script>

<AdminPage title="Recorders" {access}>
	{#if recorders.length === 0}
		<p>No recorders yet. A recorder is added from the server's shell: <code>radioarchive recorder add "North hill"</code>.</p>
	{:else}
		<p class="meta">Times are {timeZone === 'UTC' ? 'UTC' : timeZone.split('/').pop()?.replaceAll('_', ' ')} time. Recorders are added and retired from the server's shell.</p>
		<ul class="recorders">
			{#each recorders as rec, i (rec.name)}
				<li class="recorder">
					<div class="head">
						<h2 id="recorder-{i}">{rec.name}</h2>
						<StateBadge state={rec.warning ? 'warning' : 'ok'} />
					</div>
					{#if rec.warnings.length}
						<ul class="warnings" aria-label="{rec.name}: warnings">
							{#each rec.warnings as w (w)}<li>{w}</li>{/each}
						</ul>
					{/if}
					<dl class="facts">
						<div>
							<dt>Connection</dt>
							<dd>
								{#if rec.connected && rec.connected_since}Connected since {when(rec.connected_since, '')}
								{:else if rec.connected}Connected
								{:else if rec.disconnected_since}Disconnected since {when(rec.disconnected_since, '')}
								{:else}Never connected{/if}
							</dd>
						</div>
						<div>
							<dt>Last connected</dt>
							<dd>{when(rec.last_connected_at, 'Never')}</dd>
						</div>
						<div>
							<dt>Last call</dt>
							<dd>{when(rec.last_call_at, 'None yet')}</dd>
						</div>
						<div>
							<dt>Client version</dt>
							<dd>
								{rec.client_version ?? 'Not known yet'}{#if rec.client_out_of_date}
									<StateBadge state="warning" text="Out of date: upgrade the recorder host" />{/if}
							</dd>
						</div>
					</dl>
					<h3>Systems</h3>
					{#if rec.systems.length === 0}
						<p class="meta">No systems attached or reported yet.</p>
					{:else}
						<ul class="systems">
							{#each rec.systems as s (s.short_name)}
								<li>
									<span class="short"><code>{s.short_name}</code>
										{#if s.system_label}<span>→ {s.system_label}</span>{:else}<span class="meta">not attached</span>{/if}
										{#if s.system_type}<span class="meta">{s.system_type}</span>{/if}</span
									>
									<span class="rate">Decode rate: {rate(s)}{#if s.deaf}
										<StateBadge state="warning" text="Near 0" />{/if}</span>
								</li>
							{/each}
						</ul>
					{/if}
					<form class="quiet" novalidate onsubmit={(e) => saveQuiet(e, rec, i)}>
						<div class="field">
							<label for={fieldID(i)}>Quiet after (minutes)</label>
							<input
								id={fieldID(i)}
								type="text"
								inputmode="numeric"
								pattern="[0-9]*"
								autocomplete="off"
								bind:value={typed[rec.name]}
								aria-invalid={problem[rec.name] ? 'true' : undefined}
								aria-describedby="{fieldID(i)}-hint{problem[rec.name] ? ` ${fieldID(i)}-error` : ''}"
							/>
							<p class="field-hint" id="{fieldID(i)}-hint">Warn when no call has come for this long. Leave it empty for off.</p>
							{#if problem[rec.name]}<p class="field-error" id="{fieldID(i)}-error">{problem[rec.name]}</p>{/if}
						</div>
						<button type="submit" class="secondary" disabled={saving[rec.name]}>Save the quiet signal</button>
						<p class="saved" role="status">{saved[rec.name] ?? ''}</p>
					</form>
				</li>
			{/each}
		</ul>
	{/if}

	<h2>Overlapping calls</h2>
	{#if overlaps.length === 0}
		<p class="meta">No two recorders recorded the same call in the last 24 h.</p>
	{:else}
		<p class="meta">Two recorders on one network both storing a call. Keep their coverage apart in trunk-recorder's config; both copies are kept.</p>
		<ul class="messages" aria-label="Overlapping calls">
			{#each overlaps as o (`${o.system_id}.${o.talkgroup}.${o.recorders.join('|')}`)}
				<li class="banner" data-tone="warning"><span>{o.message}{#if new Set(overlaps.map((x) => x.system_id)).size > 1}<span class="meta"> ({o.system_label})</span>{/if}</span></li>
			{/each}
		</ul>
	{/if}

	<h2>Rejected calls</h2>
	{#if rejected.length === 0}
		<p class="meta">No calls rejected from a recorder's system that isn't attached.</p>
	{:else}
		<ul class="messages" aria-label="Rejected calls">
			{#each rejected as j (`${j.recorder}|${j.short_name}`)}
				<li>{j.message}{#if j.last_at}<span class="meta">, the last {when(j.last_at, '')}</span>{/if}</li>
			{/each}
		</ul>
		<p class="meta">Attach the recorder's system to keep its calls. Only calls after that are kept.</p>
	{/if}
</AdminPage>

<style>
	.meta {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.recorders,
	.warnings,
	.systems,
	.messages {
		list-style: none;
		margin: 0;
		padding: 0;
	}
	.recorders {
		display: flex;
		flex-direction: column;
		gap: var(--space-lg);
		margin-bottom: var(--space-xl);
	}
	.recorder {
		padding: var(--space-lg);
		background: var(--surface);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
		overflow-wrap: anywhere;
	}
	.head {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-sm);
		margin-bottom: var(--space-sm);
	}
	.head h2 {
		margin: 0;
	}
	h3 {
		margin: var(--space-md) 0 var(--space-xs);
		font-size: var(--text-md);
	}
	.warnings li {
		margin: 0 0 var(--space-xs);
		padding: var(--space-xs) var(--space-md);
		background: var(--warn-bg);
		color: var(--warn-fg);
		border-radius: var(--radius);
	}
	.messages .banner .meta {
		color: inherit;
	}
	.facts {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(var(--field-min), 1fr));
		gap: var(--space-sm) var(--space-lg);
		margin: var(--space-md) 0 0;
	}
	.facts dt {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.facts dd {
		margin: 0;
	}
	.systems li {
		display: flex;
		flex-wrap: wrap;
		justify-content: space-between;
		gap: var(--space-2xs) var(--space-md);
		padding: var(--space-xs) 0;
		border-bottom: var(--border-width) solid var(--border);
	}
	.short {
		display: inline-flex;
		flex-wrap: wrap;
		gap: var(--space-xs);
		align-items: baseline;
	}
	.quiet {
		max-width: var(--measure);
		margin-top: var(--space-md);
	}
	.quiet input {
		width: var(--field-narrow);
	}
	.saved {
		margin: var(--space-xs) 0 0;
		font-size: var(--text-sm);
	}
	.saved:empty {
		display: none;
	}
	.messages {
		max-width: var(--measure);
		margin-bottom: var(--space-xl);
	}
	.messages li {
		margin: 0 0 var(--space-xs);
	}
</style>
