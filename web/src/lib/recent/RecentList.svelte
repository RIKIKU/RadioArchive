<script lang="ts">
	// The recent calls list (SPEC.md R24, R46, R62, R63, R73; ADR 0013, ADR
	// 0016): a search's calls, a page at a time, with Load more. The Recent
	// calls page shows it under its filters; the call card (#80) under a
	// call, filtered to its talkgroup and scrolled to its time.
	//
	// Each row: the talkgroup's name (R24) and group, the start time on the
	// deployment's clock (TZ), the duration, and the units heard, each by
	// its alias, else its radio ID (R27). Pressing the call replays it in the dock
	// (R46), which pauses live listening; a call still in progress has no
	// recording yet, so it offers live listening instead. Download saves the
	// stored file under R63's name (ADR 0014). Share (ADR 0017) and Add to
	// basket (ADR 0018) go in the row's actions, after Download (actions). Under
	// a call card, the card's call is marked "This call" (current).
	import type { Snippet } from 'svelte';
	import { Icon } from 'archiveux/components';
	import type { Player } from '#lib/audio/player.svelte.ts';
	import type { Index } from '#lib/listening/set.ts';
	import {
		callInfoOf,
		clock,
		nameOf,
		requestOf,
		startOf,
		talkgroupOf,
		unitsOf,
		type RecentCall,
		type RecentPage,
		type Refusal,
		type Search
	} from './search.ts';

	let {
		search,
		ix,
		timeZone,
		player,
		label = 'Recent calls',
		onrefusal,
		onlive,
		actions,
		current
	}: {
		/** What to list; undefined: talkgroups were named and none is public, so none. */
		search: Search | undefined;
		ix: Index;
		timeZone: string;
		player: Player;
		/** The list's accessible name. */
		label?: string;
		/** A refusal (R78) or a failure, for the page to show under its form; without it the list shows it. undefined: none now. */
		onrefusal?: (r: Refusal | undefined) => void;
		/** Listen live to a call in progress. */
		onlive?: (c: RecentCall) => void;
		/** More actions for a row, after Download. */
		actions?: Snippet<[RecentCall]>;
		/** The call card's call, marked in the list. */
		current?: string;
	} = $props();

	let calls: RecentCall[] = $state([]);
	let next: string | null = $state(null);
	let phase: 'loading' | 'more' | 'done' | 'failed' = $state('loading');
	let refusal: Refusal | undefined = $state();
	let asked = 0;

	const failed: Refusal = { error: 'failed', message: "Couldn't load recent calls. Try again." };

	async function page(s: Search, cursor: string | undefined): Promise<RecentPage | Refusal> {
		const { url, init } = requestOf(s, cursor);
		try {
			const r = await fetch(url, init);
			const body = await r.json().catch(() => undefined);
			if (r.ok && body && Array.isArray(body.calls)) return body as RecentPage;
			if (body && typeof body.error === 'string' && typeof body.message === 'string') return body as Refusal;
		} catch {
			// The network: said below.
		}
		return failed;
	}

	function refuse(r: Refusal | undefined) {
		refusal = r;
		onrefusal?.(r);
	}

	// A new search starts the list again.
	$effect(() => {
		const s = search;
		const n = ++asked;
		calls = [];
		next = null;
		refuse(undefined);
		if (!s) {
			phase = 'done';
			return;
		}
		phase = 'loading';
		void page(s, undefined).then((got) => {
			if (n !== asked) return;
			if ('error' in got) {
				phase = 'failed';
				refuse(got);
				return;
			}
			calls = got.calls;
			next = got.next;
			phase = 'done';
		});
	});

	async function more() {
		const s = search;
		if (!s || !next || phase !== 'done') return;
		const n = asked;
		phase = 'more';
		const got = await page(s, next);
		if (n !== asked) return;
		if ('error' in got) {
			phase = 'done';
			refuse(got);
			return;
		}
		refuse(undefined);
		// A call is listed once, whatever happened between the pages.
		const seen = new Set(calls.map((c) => c.id));
		calls = [...calls, ...got.calls.filter((c) => !seen.has(c.id))];
		next = got.next;
		phase = 'done';
	}

	const several = $derived(!ix.single);

	function play(c: RecentCall) {
		if (!c.recording) return;
		player.replayCall(callInfoOf(ix, c), c.recording.url);
	}

	const downloadURL = (c: RecentCall) => `/api/v1/calls/${encodeURIComponent(c.id)}/download`;
	const unitsText = (c: RecentCall) => {
		const us = unitsOf(c);
		return us.length === 0 ? 'No unit heard' : `${us.length === 1 ? 'Unit' : 'Units'} ${us.join(', ')}`;
	};
	const playing = $derived(player.replay.call?.callId);
</script>

{#if phase === 'loading'}
	<p class="status" role="status">Loading recent calls…</p>
{:else if refusal && !onrefusal && !calls.length}
	<p class="error" role="alert">{refusal.message}</p>
{:else if phase !== 'failed' && !calls.length}
	<p class="status" role="status">No calls match. Try other filters or a wider time range.</p>
{/if}

{#if calls.length}
	<ul class="calls" aria-label={label}>
		{#each calls as c (c.id)}
			{@const t = talkgroupOf(ix, c)}
			{@const start = startOf(c.started_at, timeZone)}
			<li class="row" data-call-id={c.id} data-playing={playing === c.id ? 'yes' : undefined} data-current={current === c.id ? 'yes' : undefined}>
				{#if c.recording}
					<button type="button" class="play" aria-label="{playing === c.id ? 'Play again' : 'Play'}: {nameOf(c)}, {start}" onclick={() => play(c)}>
						<Icon name="play" />
					</button>
				{:else}
					<button type="button" class="play" aria-label="Listen live: {nameOf(c)}, in progress" onclick={() => onlive?.(c)} disabled={!onlive}>
						<Icon name="play" />
					</button>
				{/if}
				<div class="what">
					<p class="name">
						{#if t?.led_colour}<span class="led" data-led={t.led_colour} aria-hidden="true"></span>{/if}
						<span class="tg">{nameOf(c)}</span>{#if t?.groups[0]}<span class="group">{t.groups[0]}</span>{/if}
						{#if several}<span class="system">{c.system.name || c.system.label}</span>{/if}
						{#if current === c.id}<span class="this">This call</span>{/if}
						{#if playing === c.id}<span class="now">Replaying</span>{/if}
					</p>
					<p class="meta">
						<time datetime={c.started_at}>{start}</time>
						<span class="sep" aria-hidden="true">·</span>
						{#if c.in_progress}<span class="progress">In progress</span>{:else}<span class="duration"><span class="visually-hidden">{'Duration '}</span>{clock(c.duration_ms ?? 0)}</span>{/if}
						<span class="sep" aria-hidden="true">·</span>
						<span class="units">{unitsText(c)}</span>
					</p>
				</div>
				<div class="row-actions">
					{#if c.recording}
						<a class="button secondary download" href={downloadURL(c)} download aria-label="Download: {nameOf(c)}, {start}"><span>Download</span></a>
					{/if}
					{@render actions?.(c)}
				</div>
			</li>
		{/each}
	</ul>
	{#if refusal && !onrefusal}<p class="error" role="alert">{refusal.message}</p>{/if}
	{#if next}
		<div class="more">
			<button type="button" class="secondary" onclick={more} disabled={phase === 'more'}>{phase === 'more' ? 'Loading more…' : 'Load more'}</button>
		</div>
	{:else}
		<p class="status end">That's every call that matches.</p>
	{/if}
{/if}

<style>
	.calls {
		list-style: none;
		margin: 0;
		padding: 0;
		border-top: var(--border-width) solid var(--border);
	}
	/* ArchiveUx's page rows: plain, a divider below. */
	.row {
		display: flex;
		align-items: center;
		gap: var(--space-xs) var(--space-md);
		padding: var(--row-pad-y) var(--row-pad-end) var(--row-pad-y) var(--row-pad-x);
		background: var(--plain-row);
		border-bottom: var(--border-width) solid var(--border);
	}
	.row:hover {
		background: var(--plain-row-hover);
	}
	.row[data-playing] {
		box-shadow: inset 0 0 0 var(--border-width-strong) var(--accent);
	}
	.play {
		flex: none;
		width: var(--target);
		padding: 0;
	}
	.what {
		flex: 1 1 auto;
		min-width: 0;
	}
	.what p {
		margin: 0;
	}
	.name {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0 var(--space-xs);
		font-weight: var(--weight-strong);
		overflow-wrap: anywhere;
	}
	.group,
	.system {
		color: var(--muted);
		font-weight: var(--weight-normal);
	}
	.group::before,
	.system::before {
		content: '·';
		margin-right: var(--space-xs);
	}
	.this {
		padding: var(--badge-pad-y) var(--badge-pad-x);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius-badge);
		color: var(--muted);
		font-size: var(--text-sm);
		font-weight: var(--weight-normal);
	}
	.now {
		padding: var(--badge-pad-y) var(--badge-pad-x);
		border-radius: var(--radius-badge);
		background: var(--accent-soft);
		color: var(--accent-soft-fg);
		font-size: var(--text-sm);
	}
	.meta {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-xs);
		color: var(--muted);
		font-family: var(--font-meta);
		font-size: var(--text-sm);
		font-variant-numeric: tabular-nums;
	}
	.row-actions {
		display: flex;
		flex: none;
		flex-wrap: wrap;
		gap: var(--space-xs);
	}
	.more {
		margin-top: var(--space-lg);
	}
	.end {
		margin-top: var(--space-md);
		color: var(--muted);
	}
	/* Phones: the actions go under the call. */
	@media (max-width: 47.99rem) {
		.row {
			flex-wrap: wrap;
		}
		.row-actions {
			flex-basis: 100%;
			padding-left: calc(var(--target) + var(--space-md));
		}
	}
</style>
