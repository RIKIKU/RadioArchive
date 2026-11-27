<script lang="ts">
	// The Listen page (R84): the listener, with the listening set.
	//
	// - Above everything, the now-playing display (the layout's,
	//   NowPlaying.svelte; R45, ADR 0051).
	// - The set (R41, R42), saved in this browser (ADR 0008), as tiles: a
	//   box with how many talkgroups are on, All on, All off and what's on,
	//   then the page's one search (R112), then a tile per group and per tag
	//   (ADR 0048). The talkgroups on, with their priority marks (R43) and
	//   the share link, are chips at the foot of the page (ADR 0051):
	//   priority first, then by agency, each agency folded until opened.
	//   They're hidden while the search has text, and kept on the page, so a
	//   chip tapped off stays where it was until the page is left.
	// - A share link (R41): /?set=… carries the set and its marks; opening
	//   one offers to replace this browser's set. What isn't public any more
	//   is dropped without a word (R33).
	// - A fireground link (R101): /?fireground=41 adds that channel's
	//   talkgroup for the session (R47), then offers Start listening. A
	//   number that names no public talkgroup, hidden, ignored or unknown,
	//   shows one "This channel isn't available", names nothing and adds
	//   nothing.
	// - Start listening (R44) is in the dock, with the player (the layout's),
	//   on every page.
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import OnList from '#lib/listening/OnList.svelte';
	import Picker from '#lib/listening/Picker.svelte';
	import Tiles from '#lib/listening/Tiles.svelte';
	import { listeningState } from '#lib/listening/state.svelte.ts';
	import {
		SHARE_PARAM,
		decodeSet,
		encodeSet,
		find,
		indexOf,
		linkEntries,
		resolve,
		sameSet,
		type Catalogue,
		type Entry,
		type Index,
		type Talkgroup
	} from '#lib/listening/set.ts';
	import { limitText, refusedBy } from '#lib/limits.ts';
	import { sendListeningSet } from '#lib/live/listening-set.ts';
	import { knowZone } from '#lib/zone.svelte.ts';

	// Shared with the account, which saves the set to it when signed in.
	const listening = listeningState();

	let ix: Index | undefined = $state();
	let loading: 'loading' | 'failed' | 'done' = $state('loading');
	// A limit's message for what failed (ADR 0039), in place of the page's own.
	let limited: string | undefined = $state();

	// A fireground link's outcome.
	type Fireground = { kind: 'added' | 'in-set'; talkgroup: Talkgroup } | { kind: 'unavailable' } | { kind: 'failed' };
	let fireground: Fireground | undefined = $state();

	// A share link waiting for the listener's answer.
	let offer: Entry[] | undefined = $state();

	let copied: '' | 'copied' | 'failed' = $state('');

	const set = $derived(ix ? resolve(ix, listening.saved) : []);
	const added = $derived(ix ? resolve(ix, listening.session).filter((r) => !listening.inSet(r.talkgroup)) : []);
	const shareLink = $derived(set.length ? `${page.url.origin}/?${SHARE_PARAM}=${encodeSet(set.map((r) => ({ system: r.talkgroup.system, id: r.talkgroup.id, priority: r.priority })))}` : '');
	const offered = $derived(ix && offer ? resolve(ix, offer) : []);

	// The live feed hears the set plus the session's additions, and hears
	// again whenever either changes (R36, R47).
	$effect(() => {
		if (!ix) return;
		sendListeningSet([...set, ...added].map((r) => ({ system: r.talkgroup.system, id: r.talkgroup.id })));
	});

	async function loadCatalogue(): Promise<Index | undefined> {
		loading = 'loading';
		try {
			const r = await fetch('/api/v1/catalogue', { headers: { Accept: 'application/json' } });
			if (!r.ok) throw await refusedBy(r);
			const cat = (await r.json()) as Catalogue & { time_zone?: string };
			knowZone(cat.time_zone);
			const next = indexOf(cat);
			listening.settle(next);
			ix = next;
			loading = 'done';
			return next;
		} catch (err) {
			limited = limitText(err);
			loading = 'failed';
			return undefined;
		}
	}

	async function openFireground(number: string, at: Index | undefined) {
		// Digits only, as the server reads them; anything else is no channel.
		if (!/^\d{1,16}$/.test(number)) {
			fireground = { kind: 'unavailable' };
			return;
		}
		try {
			const r = await fetch(`/api/v1/fireground/${number}`, { headers: { Accept: 'application/json' } });
			if (r.status === 404) {
				fireground = { kind: 'unavailable' };
				return;
			}
			if (!r.ok) throw await refusedBy(r);
			const body = (await r.json()) as { talkgroup: Talkgroup };
			const t = (at && find(at, body.talkgroup)) ?? body.talkgroup;
			fireground = { kind: listening.addForSession(t), talkgroup: t };
		} catch (err) {
			limited = limitText(err);
			fireground = { kind: 'failed' };
		}
	}

	function openShare(text: string, at: Index) {
		const entries = linkEntries(at, decodeSet(text));
		// A link with nothing public left in it, or with this browser's set
		// already, asks nothing.
		if (!entries.length || sameSet(at, entries, listening.saved)) {
			forgetShare();
			return;
		}
		offer = entries;
	}

	// The link has been answered: reloading the page doesn't ask again.
	function forgetShare() {
		offer = undefined;
		const url = new URL(page.url.href);
		if (!url.searchParams.has(SHARE_PARAM)) return;
		url.searchParams.delete(SHARE_PARAM);
		try {
			replaceState(url, page.state);
		} catch {
			// The router isn't ready: the link stays in the address and asks again on reload.
		}
	}

	function acceptShare() {
		if (offer) listening.replace(offer);
		forgetShare();
	}

	async function copy() {
		try {
			await navigator.clipboard.writeText(shareLink);
			copied = 'copied';
		} catch {
			copied = 'failed';
		}
	}

	onMount(async () => {
		const params = page.url.searchParams;
		const at = await loadCatalogue();
		const share = params.get(SHARE_PARAM);
		if (share !== null && at) openShare(share, at);
		const number = params.get('fireground');
		if (number !== null) await openFireground(number, at);
	});

	const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? '' : 's'}`;

	// The talkgroups on, as chips, grouped by agency and folded away per
	// agency. Kept on the page once the set has had anything in it, so a
	// last chip tapped off can be tapped back on.
	let hadSet = $state(false);
	// The one search's text; while it has any, the talkgroups on are hidden.
	let query = $state('');
	$effect(() => {
		if (set.length) hadSet = true;
	});
</script>

<svelte:head><title>Listen · RadioArchive</title></svelte:head>

<h1>Listen</h1>

{#if fireground?.kind === 'unavailable'}
	<p class="alert" role="status">This channel isn't available.</p>
{:else if fireground?.kind === 'failed'}
	<p class="error" role="alert">{limited ?? "Couldn't look up that channel. Reload the page to try again."}</p>
{:else if fireground}
	<p class="ok" role="status">
		{#if fireground.kind === 'added'}
			<strong>{fireground.talkgroup.name}</strong> is added for this session. Start listening to hear it.
		{:else}
			<strong>{fireground.talkgroup.name}</strong> is in your listening set. Start listening to hear it.
		{/if}
	</p>
{/if}

{#if offer && offered.length}
	<section class="offer" aria-labelledby="offer-heading">
		<h2 id="offer-heading">Open this shared listening set?</h2>
		<p>
			It has {plural(offered.length, 'talkgroup')}{#if offered.some((r) => r.priority)}, {plural(offered.filter((r) => r.priority).length, 'priority talkgroup')}{/if}.
			It replaces the {plural(set.length, 'talkgroup')} in your listening set in this browser.
		</p>
		<ul class="names">
			{#each offered as r (`${r.talkgroup.system ?? 0}.${r.talkgroup.id}`)}
				<li>{r.talkgroup.name}{#if r.priority}<span class="mark">Priority</span>{/if}</li>
			{/each}
		</ul>
		<div class="actions">
			<button type="button" onclick={acceptShare}>Replace my listening set</button>
			<button type="button" class="secondary" onclick={forgetShare}>Keep mine</button>
		</div>
	</section>
{/if}

{#if loading === 'loading'}
	<p class="status" role="status">Loading talkgroups…</p>
{:else if loading === 'failed'}
	<p class="error" role="alert">{limited ?? "Couldn't load the talkgroups."}</p>
	<button type="button" class="secondary" onclick={loadCatalogue}>Try again</button>
{:else if ix}
	{#if added.length}
		<section aria-labelledby="session-heading">
			<h2 id="session-heading">Added for this session</h2>
			<p class="hint">Until you close this tab. Keep one to add it to your listening set.</p>
			<ul class="rows" aria-label="Added for this session">
				{#each added as r (`${r.talkgroup.system ?? 0}.${r.talkgroup.id}`)}
					<li class="row">
						<span class="name">{r.talkgroup.name}</span>
						{#if r.talkgroup.label && r.talkgroup.label !== r.talkgroup.name}<span class="meta">{r.talkgroup.label}</span>{/if}
						<span class="row-actions">
							<button type="button" class="secondary" aria-label="Keep {r.talkgroup.name}" onclick={() => listening.keep(r.talkgroup)}>Keep</button>
						</span>
					</li>
				{/each}
			</ul>
		</section>
	{/if}

	<section aria-labelledby="set-heading">
		<h2 id="set-heading">Your listening set</h2>
		<!-- The one search: finds, switches on and off and marks priority. -->
		<Tiles {ix} {listening}><Picker {ix} {listening} bind:query /></Tiles>
	</section>

	{#if set.length || hadSet}
		<!-- Hidden, not dropped, while searching: what was tapped off stays. -->
		<section class="on-list" aria-labelledby="on-heading" hidden={!!query.trim()}>
			<h2 id="on-heading">Talkgroups on <span class="count">({set.length.toLocaleString('en-AU')})</span></h2>
			<OnList {ix} {listening} />
			{#if shareLink}
				<div class="share">
					<div class="field">
						<label for="share-link">Share link</label>
						<input id="share-link" type="text" readonly value={shareLink} aria-describedby="share-hint" onfocus={(e) => e.currentTarget.select()} />
						<p class="field-hint" id="share-hint">Opens this set, with its priority marks, on another device or from a bookmark.</p>
					</div>
					<button type="button" class="secondary" onclick={copy}>Copy share link</button>
					<p class="status copied" role="status">{copied === 'copied' ? 'Link copied.' : copied === 'failed' ? "Couldn't copy it. Copy the link from the box." : ''}</p>
				</div>
			{/if}
		</section>
	{/if}
{/if}

<style>
	section {
		margin-top: var(--space-2xl);
	}
	.alert {
		margin: 0 0 var(--space-md);
		padding: var(--space-sm) var(--space-md);
		background: var(--warn-bg);
		color: var(--warn-fg);
		border-radius: var(--radius);
	}
	.ok {
		margin: 0 0 var(--space-md);
	}
	.offer {
		padding: var(--space-lg);
		background: var(--surface);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
	}
	.offer h2 {
		margin-top: 0;
	}
	.names {
		margin: var(--space-sm) 0 var(--space-lg);
		padding-left: var(--space-xl);
	}
	.actions,
	.row-actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-sm);
	}
	.rows {
		list-style: none;
		margin: 0;
		padding: 0;
		border-top: var(--border-width) solid var(--border);
	}
	.row {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-xs) var(--space-md);
		padding: var(--space-sm) var(--row-pad-end) var(--space-sm) var(--row-pad-x);
		background: var(--plain-row);
		border-bottom: var(--border-width) solid var(--border);
	}
	.row:hover {
		background: var(--plain-row-hover);
	}
	.name {
		font-weight: var(--weight-strong);
		overflow-wrap: anywhere;
	}
	.meta,
	.hint {
		color: var(--muted);
		font-family: var(--font-meta);
		font-size: var(--text-sm);
	}
	.mark {
		margin-left: var(--space-sm);
		color: var(--muted);
		font-size: var(--text-sm);
		font-weight: var(--weight-strong);
	}
	.row-actions {
		margin-left: auto;
	}
	.share {
		margin-top: var(--space-lg);
		max-width: var(--field-search);
	}
	.copied:empty {
		padding: 0;
	}
	.on-list {
		margin-top: var(--space-2xl);
	}
	.on-list .count {
		color: var(--muted);
		font-weight: var(--weight-normal);
		font-variant-numeric: tabular-nums;
	}
</style>
