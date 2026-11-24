<script lang="ts">
	// A call link (SPEC.md R24, R33, R45-R47, R60-R62, R88; ADR 0004, 0008,
	// 0016, 0017): /calls/<call ID> opens the radio with the call's card.
	//
	// - The card: the talkgroup's name (R24) and group, the start date and
	//   time on the deployment's clock (TZ), the duration, the units heard
	//   (by radio ID; #83 adds aliases), Play, Download and Share. Live
	//   listening doesn't start; Start listening stays in the dock.
	// - In progress: the card says so and offers live listening, which adds
	//   the talkgroup for the session (R47). The page asks again every few
	//   seconds, so once the call ends and its recording arrives the same
	//   card plays it; opening the link again later does too.
	// - Unavailable (deleted, never stored, hidden, ignored, patched with
	//   either, within its delay, not a call ID): one "This call isn't
	//   available", naming nothing (R33, R61). The server's answer for the
	//   page is the app's own page, 404, whatever the reason (ADR 0017).
	// - Below the card: recent calls on its talkgroup from its start, oldest
	//   first, so the list opens at the call (ADR 0016). Play from here
	//   waits on #75's open question 3 (#92).
	import { onDestroy, onMount } from 'svelte';
	import { page } from '$app/state';
	import { Icon } from 'archiveux/components';
	import RecentList from '#lib/recent/RecentList.svelte';
	import ShareButton from '#lib/share/ShareButton.svelte';
	import { usePlayer } from '#lib/audio/context.ts';
	import { listeningState } from '#lib/listening/state.svelte.ts';
	import { indexOf, keyOf, resolve, type Catalogue, type Index } from '#lib/listening/set.ts';
	import { sendListeningSet } from '#lib/live/listening-set.ts';
	import {
		callInfoOf,
		clock,
		dateTimeOf,
		nameOf,
		startOf,
		talkgroupOf,
		unitsOf,
		zoneLabel,
		type RecentCall,
		type Search
	} from '#lib/recent/search.ts';

	/** How often a call in progress, or one waiting for its recording, is asked about again. */
	const POLL_MS = 5000;

	const listening = listeningState();
	const player = usePlayer();

	let ix: Index | undefined = $state.raw();
	let timeZone = $state('UTC');
	// The call, as the call details give it; null: unavailable.
	let call: RecentCall | null | undefined = $state.raw();
	let phase: 'loading' | 'failed' | 'done' = $state('loading');
	// What Listen live did: added for the session, or already in the set.
	let live: '' | 'added' | 'in-set' = $state('');

	const id = $derived(page.params.id ?? '');

	async function catalogue(): Promise<Index> {
		const r = await fetch('/api/v1/catalogue', { headers: { Accept: 'application/json' } });
		if (!r.ok) throw new Error(String(r.status));
		const c = (await r.json()) as Catalogue & { time_zone?: string };
		const next = indexOf(c);
		listening.settle(next);
		timeZone = c.time_zone || 'UTC';
		return next;
	}

	async function details(callId: string): Promise<RecentCall | null> {
		const r = await fetch('/api/v1/calls', {
			method: 'POST',
			headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
			body: JSON.stringify({ ids: [callId] })
		});
		if (!r.ok) throw new Error(String(r.status));
		const body = (await r.json()) as { calls: RecentCall[] };
		return body.calls.find((c) => c.id === callId) ?? null;
	}

	let timer: ReturnType<typeof setTimeout> | undefined;
	let asked = 0;
	onDestroy(() => clearTimeout(timer));

	async function load(callId: string) {
		const n = ++asked;
		clearTimeout(timer);
		phase = 'loading';
		call = undefined;
		live = '';
		try {
			const [next, c] = await Promise.all([ix ? Promise.resolve(ix) : catalogue(), details(callId)]);
			if (n !== asked) return;
			ix = next;
			call = c;
			phase = 'done';
			poll(callId, n);
		} catch {
			if (n === asked) phase = 'failed';
		}
	}

	// A call in progress, or ended with its recording on its way, is asked
	// about again until it has its recording or isn't available.
	function poll(callId: string, n: number) {
		if (!call || call.recording) return;
		timer = setTimeout(async () => {
			try {
				const c = await details(callId);
				if (n !== asked) return;
				call = c;
			} catch {
				// Asked again next time.
			}
			if (n === asked) poll(callId, n);
		}, POLL_MS);
	}

	onMount(() => {
		void load(id);
	});
	let shown = '';
	$effect(() => {
		const next = id;
		if (shown && next !== shown) void load(next);
		shown = next;
	});

	// The listening set, with the session's additions: what the live feed
	// hears, here as on Listen and Recent calls.
	const heard = $derived.by(() => {
		if (!ix) return [];
		const seen = new Set<string>();
		return [...resolve(ix, listening.saved), ...resolve(ix, listening.session)]
			.map((r) => r.talkgroup)
			.filter((t) => !seen.has(keyOf(t)) && !!seen.add(keyOf(t)));
	});
	$effect(() => {
		if (!ix) return;
		sendListeningSet(heard.map((t) => ({ system: t.system, id: t.id })));
	});

	const t = $derived(ix && call ? talkgroupOf(ix, call) : undefined);
	const name = $derived(call ? nameOf(call) : '');
	const units = $derived(call ? unitsOf(call) : []);
	// Below the card: its talkgroup's calls from its start, oldest first.
	const below: Search | undefined = $derived(call && t ? { talkgroups: [keyOf(t)], from: call.started_at, order: 'oldest' } : undefined);
	const allHref = $derived(t ? `/recent?talkgroups=${encodeURIComponent(keyOf(t))}` : '/recent');
	const playing = $derived(call ? player.replay.call?.callId === call.id : false);

	function play() {
		if (!ix || !call?.recording) return;
		player.replayCall(callInfoOf(ix, call), call.recording.url);
	}

	// Live listening: the call's talkgroup is heard, added for the session
	// if it isn't in the set (R47), and listening starts or resumes.
	function listenLive(c: RecentCall) {
		const tg = ix && talkgroupOf(ix, c);
		if (!tg) return;
		const how = listening.inSet(tg) ? 'in-set' : listening.addForSession(tg);
		if (c.id === call?.id) live = how;
		if (player.phase === 'stopped') void player.start();
		else if (player.paused) void player.resume();
	}

	const downloadURL = (c: RecentCall) => `/api/v1/calls/${encodeURIComponent(c.id)}/download`;
</script>

<svelte:head>
	<title>{call ? `${name} · RadioArchive` : call === null ? "Call isn't available · RadioArchive" : 'Call · RadioArchive'}</title>
</svelte:head>

{#if phase === 'loading'}
	<p class="status" role="status">Loading the call…</p>
{:else if phase === 'failed'}
	<p class="error" role="alert">Couldn't load this call.</p>
	<button type="button" class="secondary" onclick={() => load(id)}>Try again</button>
{:else if call === null}
	<section class="card unavailable" aria-labelledby="call-heading">
		<h1 id="call-heading">This call isn't available</h1>
		<p>Recent calls and live listening are still here.</p>
		<p class="links"><a href="/recent">Recent calls</a><a href="/">Listen</a></p>
	</section>
{:else if call && ix}
	<article class="card" aria-labelledby="call-heading" data-call-id={call.id} data-state={call.in_progress ? 'in-progress' : call.recording ? 'recorded' : 'ended'}>
		<p class="kicker">Call</p>
		<h1 id="call-heading">
			{#if t?.led_colour}<span class="led" data-led={t.led_colour} aria-hidden="true"></span>{/if}
			<span class="tg">{name}</span>
		</h1>
		{#if t?.groups[0] || !ix.single}
			<p class="where">
				{#if t?.groups[0]}<span class="group">{t.groups.join(', ')}</span>{/if}
				{#if !ix.single}<span class="system">{call.system.name || call.system.label}</span>{/if}
			</p>
		{/if}
		<dl class="facts">
			<div>
				<dt>Started</dt>
				<dd><time datetime={call.started_at}>{dateTimeOf(call.started_at, timeZone)}</time> <span class="zone">{zoneLabel(timeZone)} time</span></dd>
			</div>
			<div>
				<dt>Duration</dt>
				<dd>
					{#if call.in_progress}<span class="progress">In progress</span>{:else}{clock(call.duration_ms ?? 0)}{/if}
				</dd>
			</div>
			<div>
				<dt>{units.length === 1 ? 'Unit heard' : 'Units heard'}</dt>
				<!-- By radio ID; #83 shows each unit's alias (R27). -->
				<dd class="units">{units.length ? units.join(', ') : 'None heard'}</dd>
			</div>
		</dl>
		<div class="actions">
			{#if call.recording}
				<button type="button" class="play" aria-label="{playing ? 'Play again' : 'Play'}: {name}, {startOf(call.started_at, timeZone)}" onclick={play}>
					<Icon name="play" /><span>{playing ? 'Play again' : 'Play'}</span>
				</button>
				<a class="button secondary" href={downloadURL(call)} download aria-label="Download: {name}, {startOf(call.started_at, timeZone)}"><span>Download</span></a>
			{:else if call.in_progress}
				<button type="button" class="play" onclick={() => call && listenLive(call)}><Icon name="play" /><span>Listen live</span></button>
			{/if}
			<ShareButton callId={call.id} label="{name}, {startOf(call.started_at, timeZone)}" />
		</div>
		{#if call.in_progress}
			<p class="note" role="status">
				{#if live === 'added'}
					Listening live. {name} is added to your listening set for this session.
				{:else if live === 'in-set'}
					Listening live. {name} is in your listening set.
				{:else}
					Once the call ends, this link plays its recording.
				{/if}
			</p>
		{:else if !call.recording}
			<p class="note" role="status">The call has ended. Its recording is on its way.</p>
		{/if}
	</article>

	<section class="more" aria-labelledby="more-heading">
		<h2 id="more-heading">More calls on {name}</h2>
		<p class="meta">From this call on, oldest first. Times are {zoneLabel(timeZone)} time. <a href={allHref}>All recent calls on {name}</a></p>
		<RecentList search={below} {ix} {timeZone} {player} label="Calls on {name}" current={call.id} onlive={listenLive}>
			{#snippet actions(c: RecentCall)}<ShareButton callId={c.id} label="{nameOf(c)}, {startOf(c.started_at, timeZone)}" />{/snippet}
		</RecentList>
	</section>
{/if}

<style>
	.card {
		max-width: var(--panel-max);
		margin: 0 0 var(--space-xl);
		padding: var(--space-lg);
		background: var(--surface);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
	}
	.card h1 {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-xs);
		margin: 0;
		overflow-wrap: anywhere;
	}
	.kicker {
		margin: 0 0 var(--space-2xs);
		color: var(--muted);
		font-size: var(--text-sm);
		font-weight: var(--weight-strong);
	}
	.where {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-xs);
		margin: var(--space-2xs) 0 0;
		color: var(--muted);
	}
	.where .group + .system::before {
		content: '·';
		margin-right: var(--space-xs);
	}
	.facts {
		display: grid;
		gap: var(--space-sm);
		margin: var(--space-lg) 0;
	}
	.facts div {
		display: grid;
		gap: var(--space-2xs);
	}
	.facts dt {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.facts dd {
		margin: 0;
		font-variant-numeric: tabular-nums;
		overflow-wrap: anywhere;
	}
	.zone {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.progress {
		padding: var(--badge-pad-y) var(--badge-pad-x);
		border-radius: var(--radius-badge);
		background: var(--accent-soft);
		color: var(--accent-soft-fg);
		font-weight: var(--weight-strong);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-sm);
	}
	.play {
		gap: var(--space-xs);
	}
	.note {
		margin: var(--space-md) 0 0;
		color: var(--muted);
	}
	.unavailable h1 {
		display: block;
	}
	.links {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-md);
		margin-bottom: 0;
	}
	.more h2 {
		margin-bottom: var(--space-xs);
	}
	.meta {
		margin: 0 0 var(--space-sm);
		color: var(--muted);
		font-family: var(--font-meta);
		font-size: var(--text-sm);
	}
	.meta a {
		font-family: var(--font-ui);
	}
</style>
