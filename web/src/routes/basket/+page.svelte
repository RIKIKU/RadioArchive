<script lang="ts">
	// The Basket page (SPEC.md P1, P4, P5, R64, R65, R67, R84, R88; ADR
	// 0004, 0005, 0015, 0018): the calls a listener has put in the download
	// basket, its conversations, and its downloads.
	//
	// - What it shows of a call comes from the call details (POST
	//   /api/v1/calls, R67), asked again as the page opens and every so
	//   often: the basket itself holds only call IDs and the listener's own
	//   names and notes. A call deleted by retention, or hidden since it was
	//   added, isn't in the answer, and shows "No longer available", the same
	//   for both, naming nothing (P1, R88). It's left out of downloads.
	// - Each call counts down to the moment the call details say retention
	//   will delete it ("deleted in 3 days").
	// - Conversations: named, with an optional note; a call can be in
	//   several; Play all plays one in order in the dock.
	// - Downloads: the whole basket or one conversation, as zips by GET (ADR
	//   0015), in "part 1 of N" when too big for one URL or over 500 calls.
	//   Download new since last time takes only the calls put in since that
	//   scope's last download; the basket stays after a download, with Clear.
	// - A live call is in the basket at once and can be downloaded once its
	//   recording has arrived: the page asks again every few seconds while a
	//   call has none.
	import { onDestroy, onMount, tick } from 'svelte';
	import { Icon } from 'archiveux/components';
	import { usePlayer } from '#lib/audio/context.ts';
	import { dockIcons } from '#lib/audio/icons.ts';
	import { listeningState } from '#lib/listening/state.svelte.ts';
	import { indexOf, keyOf, resolve, type Catalogue, type Index } from '#lib/listening/set.ts';
	import { sendListeningSet } from '#lib/live/listening-set.ts';
	import { basketState } from '#lib/basket/state.svelte.ts';
	import {
		MAX_NAME,
		MAX_NOTE,
		callsIn,
		conversationID,
		conversationProblems,
		countdownOf,
		lastOf,
		listOf,
		looseOf,
		roomForConversation,
		zipParts,
		type BasketCall,
		type Conversation,
		type Scope,
		type ZipPart
	} from '#lib/basket/basket.ts';
	import { callInfoOf, clock, nameOf, startOf, talkgroupOf, unitsOf, zoneLabel, type RecentCall } from '#lib/recent/search.ts';

	/** How often calls waiting for their recording are asked about again; and the rest. */
	const POLL_MS = 5000;
	const REFRESH_MS = 60_000;
	/** The most IDs one call list request names (R73). */
	const PER_REQUEST = 500;

	const listening = listeningState();
	const player = usePlayer();
	const basket = basketState();
	const b = $derived(basket.basket);

	let ix: Index | undefined = $state.raw();
	let timeZone = $state('UTC');
	let loading: 'loading' | 'failed' | 'done' = $state('loading');
	// Each call's details, as last asked; null: unavailable. Missing: not asked yet.
	let details: Map<string, RecentCall | null> = $state.raw(new Map());
	let checkFailed = $state(false);
	let now = $state(Date.now());

	async function loadCatalogue() {
		loading = 'loading';
		try {
			const r = await fetch('/api/v1/catalogue', { headers: { Accept: 'application/json' } });
			if (!r.ok) throw new Error(String(r.status));
			const c = (await r.json()) as Catalogue & { time_zone?: string };
			const next = indexOf(c);
			listening.settle(next);
			timeZone = c.time_zone || 'UTC';
			ix = next;
			loading = 'done';
		} catch {
			loading = 'failed';
		}
	}

	/** The availability check (R64, R67): the call details of ids, null for each unavailable one. */
	async function check(ids: string[]): Promise<Map<string, RecentCall | null>> {
		const out = new Map<string, RecentCall | null>();
		for (let i = 0; i < ids.length; i += PER_REQUEST) {
			const chunk = ids.slice(i, i + PER_REQUEST);
			const r = await fetch('/api/v1/calls', {
				method: 'POST',
				headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
				body: JSON.stringify({ ids: chunk })
			});
			if (!r.ok) throw new Error(String(r.status));
			const body = (await r.json()) as { calls: RecentCall[] };
			const got = new Map(body.calls.map((c) => [c.id, c]));
			for (const id of chunk) out.set(id, got.get(id) ?? null);
		}
		return out;
	}

	let timer: ReturnType<typeof setTimeout> | undefined;
	let asking = false;
	onDestroy(() => clearTimeout(timer));

	/** Asks about every call in the basket (all), or only those not asked about yet. */
	async function refresh(all: boolean) {
		if (asking) return;
		const ids = b.calls.map((c) => c.id).filter((id) => all || !details.has(id));
		clearTimeout(timer);
		if (ids.length) {
			asking = true;
			try {
				const got = await check(ids);
				const next = new Map(all ? [] : details);
				for (const [id, c] of got) next.set(id, c);
				details = next;
				checkFailed = false;
			} catch {
				checkFailed = true;
			} finally {
				asking = false;
			}
		}
		now = Date.now();
		const waiting = [...details.values()].some((c) => c && !c.recording);
		timer = setTimeout(() => void refresh(true), waiting || checkFailed ? POLL_MS : REFRESH_MS);
	}

	onMount(() => {
		void loadCatalogue();
		void refresh(true);
	});
	// A call put in the basket here or in another tab is asked about at once.
	$effect(() => {
		if (b.calls.some((c) => !details.has(c.id))) void refresh(false);
	});

	// The listening set, with the session's additions: what the live feed
	// hears, here as on every page.
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

	/** Whether a call can be downloaded now: available and recorded. */
	const ready = (id: string): boolean => !!details.get(id)?.recording;
	const startOfId = (id: string) => details.get(id)?.started_at;
	const loose = $derived(looseOf(b));

	// Downloads (R64, R65): for each scope, its zips whole and new since.
	const scopeKey = (s: Scope) => (s.kind === 'basket' ? 'basket' : `c-${s.id}`);
	interface Offer {
		all: ZipPart[];
		fresh: ZipPart[];
		allCalls: number;
		freshCalls: number;
		last?: number;
	}
	function offerOf(s: Scope): Offer {
		const all = listOf(b, s, ready);
		const fresh = listOf(b, s, ready, true);
		return { all: zipParts(all), fresh: zipParts(fresh), allCalls: callsIn(all), freshCalls: callsIn(fresh), last: lastOf(b, s) };
	}
	const offers = $derived.by(() => {
		const m = new Map<string, Offer>();
		m.set('basket', offerOf({ kind: 'basket' }));
		for (const v of b.conversations) m.set(`c-${v.id}`, offerOf({ kind: 'conversation', id: v.id }));
		return m;
	});
	// A download in parts, shown as its list of parts.
	let inParts: { key: string; which: 'all' | 'fresh' } | undefined = $state();
	function downloaded(s: Scope) {
		basket.downloaded(s);
	}

	// Conversations.
	let creating = $state(false);
	let editing: string | undefined = $state();
	let name = $state('');
	let note = $state('');
	let errors: { name?: string; note?: string } = $state({});
	const problems = $derived(Object.values(errors).filter(Boolean).length);

	async function startNew() {
		editing = undefined;
		creating = true;
		name = '';
		note = '';
		errors = {};
		await tick();
		document.getElementById('conversation-name')?.focus();
	}
	async function startEdit(v: Conversation) {
		creating = false;
		editing = v.id;
		name = v.name;
		note = v.note ?? '';
		errors = {};
		await tick();
		document.getElementById('conversation-name')?.focus();
	}
	function cancel() {
		creating = false;
		editing = undefined;
		errors = {};
	}
	async function save(e: SubmitEvent) {
		e.preventDefault();
		errors = conversationProblems(name, note);
		if (problems) {
			await tick();
			document.querySelector<HTMLElement>('.conversation-form [aria-invalid="true"]')?.focus();
			return;
		}
		if (editing) basket.editConversation(editing, name, note);
		else basket.newConversation(conversationID(), name, note);
		cancel();
	}

	let deleting: string | undefined = $state();
	let clearing = $state(false);

	function put(convId: string, id: string) {
		if (convId) basket.putInConversation(convId, id, startOfId);
	}

	function playAll(v: Conversation) {
		if (!ix) return;
		const calls = v.calls
			.map((c) => details.get(c.id))
			.filter((c): c is RecentCall => !!c?.recording)
			.map((c) => ({ info: callInfoOf(ix!, c), url: c.recording!.url }));
		player.replayAll(v.name, calls);
	}
	function play(c: RecentCall) {
		if (!ix || !c.recording) return;
		player.replayCall(callInfoOf(ix, c), c.recording.url);
	}

	const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;
	const unitsText = (c: RecentCall) => {
		const us = unitsOf(c);
		return us.length === 0 ? 'No unit heard' : `${us.length === 1 ? 'Unit' : 'Units'} ${us.join(', ')}`;
	};
	const downloadURL = (c: RecentCall) => `/api/v1/calls/${encodeURIComponent(c.id)}/download`;
	const lastText = (t: number | undefined) => (t ? `Last downloaded ${startOf(new Date(t).toISOString(), timeZone)}` : 'Not downloaded yet');
	const playingId = $derived(player.replay.call?.callId);
	const gone = $derived(b.calls.filter((c) => details.get(c.id) === null).length);
</script>

<svelte:head><title>Basket · RadioArchive</title></svelte:head>

<h1>Basket</h1>

{#snippet downloads(s: Scope, what: string)}
	{@const o = offers.get(scopeKey(s))}
	{#if o}
		<div class="downloads" role="group" aria-label="Download {what}">
			{#if o.allCalls === 0}
				<p class="meta">Nothing to download yet.</p>
			{:else}
				{#each [{ which: 'all' as const, parts: o.all, calls: o.allCalls, text: o.last ? `Download ${what} again` : `Download ${what}` }, ...(o.last && o.freshCalls ? [{ which: 'fresh' as const, parts: o.fresh, calls: o.freshCalls, text: 'Download new since last time' }] : [])] as d (d.which)}
					{#if d.parts.length === 1}
						<a class="button {d.which === 'all' && o.last ? 'secondary' : ''}" href={d.parts[0].url} download onclick={() => downloaded(s)}
							><Icon svg={dockIcons.download} /><span>{d.text}</span><span class="count">({plural(d.calls, 'call')})</span></a
						>
					{:else}
						{@const open = inParts?.key === scopeKey(s) && inParts.which === d.which}
						<button type="button" class={d.which === 'all' && o.last ? 'secondary' : ''} aria-expanded={open} onclick={() => (inParts = open ? undefined : { key: scopeKey(s), which: d.which })}
							><Icon svg={dockIcons.download} /><span>{d.text}</span><span class="count">({plural(d.calls, 'call')}, {d.parts.length} parts)</span></button
						>
					{/if}
				{/each}
				{#if o.last && !o.freshCalls}<p class="meta">Nothing new since last time.</p>{/if}
			{/if}
			<p class="meta last">{lastText(o.last)}</p>
		</div>
		{#if inParts?.key === scopeKey(s)}
			{@const parts = inParts.which === 'all' ? o.all : o.fresh}
			<section class="parts" aria-label="{what}, in parts">
				<p>Too big for one download, so it comes in {parts.length} zips. Download each one:</p>
				<ol>
					{#each parts as p (p.part)}
						<li><a class="button secondary" href={p.url} download onclick={() => downloaded(s)}><span>Part {p.part} of {p.parts}</span><span class="count">({plural(p.calls, 'call')})</span></a></li>
					{/each}
				</ol>
			</section>
		{/if}
	{/if}
{/snippet}

{#snippet item(bc: BasketCall, conv: Conversation | undefined)}
	{@const c = details.get(bc.id)}
	{#if c === undefined}
		<li class="item" data-call-id={bc.id} data-state="checking"><p class="status">Checking…</p></li>
	{:else if c === null}
		<!-- Deleted, or hidden since it was added: one look, naming nothing (P1, R88). -->
		<li class="item gone" data-call-id={bc.id} data-state="unavailable">
			<p class="gone-text">No longer available</p>
			<div class="item-actions">
				{#if conv}
					<button type="button" class="ghost" aria-label="Take out of {conv.name}: a call no longer available" onclick={() => basket.takeOutOfConversation(conv.id, bc.id)}>Take out</button>
				{:else}
					<button type="button" class="ghost" aria-label="Remove from the basket: a call no longer available" onclick={() => basket.remove(bc.id)}>Remove</button>
				{/if}
			</div>
		</li>
	{:else if ix}
		{@const t = talkgroupOf(ix, c)}
		{@const start = startOf(c.started_at, timeZone)}
		{@const left = countdownOf(c.deletes_at, now)}
		{@const others = b.conversations.filter((v) => v.id !== conv?.id && !v.calls.some((x) => x.id === c.id))}
		<li class="item" data-call-id={c.id} data-state={c.recording ? 'recorded' : 'waiting'} data-playing={playingId === c.id ? 'yes' : undefined}>
			{#if c.recording}
				<button type="button" class="play" aria-label="{playingId === c.id ? 'Play again' : 'Play'}: {nameOf(c)}, {start}" onclick={() => play(c)}><Icon name="play" /></button>
			{:else}
				<span class="play-place" aria-hidden="true"></span>
			{/if}
			<div class="what">
				<p class="name">
					{#if t?.led_colour}<span class="led" data-led={t.led_colour} aria-hidden="true"></span>{/if}
					<span class="tg">{nameOf(c)}</span>{#if t?.groups[0]}<span class="group">{t.groups[0]}</span>{/if}
					{#if !ix.single}<span class="system">{c.system.name || c.system.label}</span>{/if}
					{#if playingId === c.id}<span class="now">Replaying</span>{/if}
				</p>
				<p class="meta">
					<time datetime={c.started_at}>{start}</time>
					<span class="sep" aria-hidden="true">·</span>
					{#if c.in_progress}<span class="progress">In progress</span>{:else}<span class="duration"><span class="visually-hidden">{'Duration '}</span>{clock(c.duration_ms ?? 0)}</span>{/if}
					<span class="sep" aria-hidden="true">·</span>
					<span class="units">{unitsText(c)}</span>
				</p>
				<p class="when">
					{#if left}<span class="countdown">{left}</span>{/if}
					{#if !c.recording}<span class="waiting">Downloadable once recorded</span>{/if}
				</p>
			</div>
			<div class="item-actions">
				{#if c.recording}
					<a class="button secondary" href={downloadURL(c)} download aria-label="Download: {nameOf(c)}, {start}"><span>Download</span></a>
				{/if}
				{#if others.length}
					<label class="visually-hidden" for="put-{conv?.id ?? 'loose'}-{c.id}">Add {nameOf(c)}, {start}, to a conversation</label>
					<select id="put-{conv?.id ?? 'loose'}-{c.id}" onchange={(e) => (put(e.currentTarget.value, c.id), (e.currentTarget.value = ''))}>
						<option value="">Add to a conversation…</option>
						{#each others as v (v.id)}<option value={v.id}>{v.name}</option>{/each}
					</select>
				{/if}
				{#if conv}
					<button type="button" class="ghost" aria-label="Take out of {conv.name}: {nameOf(c)}, {start}" onclick={() => basket.takeOutOfConversation(conv.id, c.id)}>Take out</button>
				{:else}
					<button type="button" class="ghost" aria-label="Remove from the basket: {nameOf(c)}, {start}" onclick={() => basket.remove(c.id)}>Remove</button>
				{/if}
			</div>
		</li>
	{/if}
{/snippet}

{#snippet form()}
	<form class="conversation-form" novalidate onsubmit={save} aria-labelledby="conversation-form-heading">
		<h3 id="conversation-form-heading">{editing ? 'Edit the conversation' : 'New conversation'}</h3>
		{#if problems}
			<p class="error" aria-live="polite">{problems === 1 ? '1 field needs attention' : `${problems} fields need attention`}</p>
		{/if}
		<div class="field">
			<label for="conversation-name">Name <span class="required">required</span></label>
			<input
				id="conversation-name"
				type="text"
				required
				autocomplete="off"
				maxlength={MAX_NAME}
				bind:value={name}
				aria-invalid={errors.name ? 'true' : undefined}
				aria-describedby="conversation-name-hint{errors.name ? ' conversation-name-error' : ''}"
				oninput={() => errors.name && (errors = { ...errors, name: undefined })}
			/>
			<p class="field-hint" id="conversation-name-hint">Such as "Structure fire". It names the conversation's folder in a download.</p>
			{#if errors.name}<p class="field-error" id="conversation-name-error">{errors.name}</p>{/if}
		</div>
		<div class="field">
			<label for="conversation-note">Note</label>
			<textarea
				id="conversation-note"
				rows="3"
				maxlength={MAX_NOTE}
				bind:value={note}
				aria-invalid={errors.note ? 'true' : undefined}
				aria-describedby="conversation-note-hint{errors.note ? ' conversation-note-error' : ''}"
				oninput={() => errors.note && (errors = { ...errors, note: undefined })}
			></textarea>
			<p class="field-hint" id="conversation-note-hint">Optional. It goes in the download's manifest.</p>
			{#if errors.note}<p class="field-error" id="conversation-note-error">{errors.note}</p>{/if}
		</div>
		<div class="actions">
			<button type="submit">{editing ? 'Save' : 'Create'}</button>
			<button type="button" class="secondary" onclick={cancel}>Cancel</button>
		</div>
	</form>
{/snippet}

{#if loading === 'failed'}
	<p class="error" role="alert">Couldn't load the talkgroups.</p>
	<button type="button" class="secondary" onclick={loadCatalogue}>Try again</button>
{:else if !b.calls.length && !b.conversations.length}
	<section class="empty" aria-labelledby="empty-heading">
		<h2 id="empty-heading">Your basket is empty</h2>
		<p>Add calls from <a href="/recent">Recent calls</a>, a call's own page, or now playing. They stay here, in this browser and in your account when you're signed in, until you take them out.</p>
	</section>
{:else}
	<section class="whole" aria-labelledby="whole-heading">
		<h2 id="whole-heading" class="visually-hidden">The whole basket</h2>
		<p class="summary">
			{plural(b.calls.length, 'call')}{#if b.conversations.length}, {plural(b.conversations.length, 'conversation')}{/if}.
			{#if gone}{plural(gone, 'call')} no longer available.{/if}
			<span class="meta">Times are {zoneLabel(timeZone)} time.</span>
		</p>
		{#if checkFailed}<p class="error" role="alert">Couldn't check the calls just now. It tries again in a moment.</p>{/if}
		{@render downloads({ kind: 'basket' }, 'the basket')}
		<div class="clear">
			{#if clearing}
				<div class="confirm" role="group" aria-labelledby="clear-heading">
					<p id="clear-heading">Clear the basket? Its {plural(b.calls.length, 'call')} and {plural(b.conversations.length, 'conversation')} are taken out. Files you've downloaded stay where they are.</p>
					<div class="actions">
						<button type="button" class="danger" onclick={() => (basket.clear(), (clearing = false))}>Clear the basket</button>
						<button type="button" class="secondary" onclick={() => (clearing = false)}>Cancel</button>
					</div>
				</div>
			{:else}
				<button type="button" class="ghost" onclick={() => (clearing = true)}>Clear</button>
			{/if}
		</div>
	</section>

	<section class="conversations" aria-labelledby="conversations-heading">
		<div class="section-head">
			<h2 id="conversations-heading">Conversations</h2>
			{#if !creating && roomForConversation(b)}<button type="button" class="secondary" onclick={startNew}>New conversation</button>{/if}
		</div>
		{#if creating}{@render form()}{/if}
		{#if !b.conversations.length && !creating}
			<p class="meta">Group calls that belong together, such as one incident's, into a named conversation. A call can be in several.</p>
		{/if}
		{#each b.conversations as v (v.id)}
			<article class="conversation" aria-labelledby="conversation-{v.id}" data-conversation={v.id}>
				{#if editing === v.id}
					{@render form()}
				{:else}
					<div class="conversation-head">
						<h3 id="conversation-{v.id}">{v.name}</h3>
						{#if v.note}<p class="note">{v.note}</p>{/if}
						<p class="meta">{plural(v.calls.length, 'call')}</p>
					</div>
					<div class="actions">
						{#if v.calls.some((c) => ready(c.id))}
							<button type="button" class="secondary" onclick={() => playAll(v)}><Icon name="play" /><span>Play all</span></button>
						{/if}
						<button type="button" class="ghost" onclick={() => startEdit(v)} aria-label="Edit: {v.name}">Edit</button>
						{#if deleting === v.id}
							<span class="confirm-inline" role="group" aria-label="Delete {v.name}?">
								<button type="button" class="danger" onclick={() => (basket.deleteConversation(v.id), (deleting = undefined))}>Delete it</button>
								<button type="button" class="secondary" onclick={() => (deleting = undefined)}>Keep it</button>
							</span>
						{:else}
							<button type="button" class="ghost" onclick={() => (deleting = v.id)} aria-label="Delete the conversation: {v.name}">Delete</button>
						{/if}
					</div>
					{#if deleting === v.id}<p class="meta">Its calls stay in the basket.</p>{/if}
				{/if}
				{@render downloads({ kind: 'conversation', id: v.id }, v.name)}
				{#if v.calls.length}
					<ul class="items" aria-label="Calls in {v.name}">
						{#each v.calls as c (c.id)}{@render item(c, v)}{/each}
					</ul>
				{:else}
					<p class="meta">No calls in it yet. Add one with "Add to a conversation" beside a call.</p>
				{/if}
			</article>
		{/each}
	</section>

	{#if loose.length}
		<section class="loose" aria-labelledby="loose-heading">
			<h2 id="loose-heading">{b.conversations.length ? 'Not in a conversation' : 'Calls'}</h2>
			<ul class="items" aria-label="Calls not in a conversation">
				{#each loose as c (c.id)}{@render item(c, undefined)}{/each}
			</ul>
		</section>
	{/if}
{/if}

<style>
	section,
	.conversation {
		margin: 0 0 var(--space-xl);
	}
	.summary {
		margin: 0 0 var(--space-sm);
	}
	.meta {
		color: var(--muted);
		font-family: var(--font-meta);
		font-size: var(--text-sm);
	}
	.downloads {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-xs) var(--space-sm);
		margin: var(--space-sm) 0;
	}
	.downloads p {
		margin: 0;
	}
	.downloads .last {
		flex-basis: 100%;
	}
	.downloads a,
	.downloads button,
	.parts a {
		gap: var(--space-xs);
	}
	.count {
		font-weight: var(--weight-normal);
	}
	.parts {
		max-width: var(--panel-max);
		margin: var(--space-sm) 0 var(--space-md);
		padding: var(--space-md);
		background: var(--surface);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
	}
	.parts p {
		margin: 0 0 var(--space-sm);
	}
	.parts ol {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-xs);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.clear {
		margin-top: var(--space-sm);
	}
	.confirm {
		max-width: var(--panel-max);
		padding: var(--space-md);
		background: var(--surface);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
	}
	.confirm p {
		margin: 0 0 var(--space-sm);
	}
	.confirm-inline {
		display: inline-flex;
		flex-wrap: wrap;
		gap: var(--space-xs);
	}
	.section-head {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-sm);
	}
	.conversation {
		padding: var(--space-lg);
		background: var(--surface);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
	}
	.conversation h3 {
		margin: 0;
		overflow-wrap: anywhere;
	}
	.conversation-head p {
		margin: var(--space-2xs) 0 0;
	}
	.note {
		white-space: pre-line;
		overflow-wrap: anywhere;
	}
	.conversation .actions {
		margin-top: var(--space-sm);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-xs) var(--space-sm);
	}
	.actions button {
		gap: var(--space-xs);
	}
	.conversation-form {
		max-width: var(--panel-max);
		margin: var(--space-md) 0;
		padding: var(--space-lg);
		background: var(--surface-2);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
	}
	.conversation .conversation-form {
		margin-top: 0;
	}
	.conversation-form h3 {
		margin-top: 0;
	}
	.conversation-form textarea {
		width: 100%;
	}
	.items {
		list-style: none;
		margin: var(--space-sm) 0 0;
		padding: 0;
		border-top: var(--border-width) solid var(--border);
	}
	/* ArchiveUx's page rows: plain, a divider below. */
	.item {
		display: flex;
		align-items: center;
		gap: var(--space-xs) var(--space-md);
		padding: var(--row-pad-y) var(--row-pad-end) var(--row-pad-y) var(--row-pad-x);
		background: var(--plain-row);
		border-bottom: var(--border-width) solid var(--border);
	}
	.item:hover {
		background: var(--plain-row-hover);
	}
	.item[data-playing] {
		box-shadow: inset 0 0 0 var(--border-width-strong) var(--accent);
	}
	.item p {
		margin: 0;
	}
	.play,
	.play-place {
		flex: none;
		width: var(--target);
		padding: 0;
	}
	.what {
		flex: 1 1 auto;
		min-width: 0;
	}
	.gone-text {
		flex: 1 1 auto;
		color: var(--muted);
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
	.now,
	.progress {
		padding: var(--badge-pad-y) var(--badge-pad-x);
		border-radius: var(--radius-badge);
		background: var(--accent-soft);
		color: var(--accent-soft-fg);
		font-size: var(--text-sm);
	}
	.item .meta {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-xs);
		font-variant-numeric: tabular-nums;
	}
	.when {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-sm);
		font-size: var(--text-sm);
	}
	.when:empty {
		display: none;
	}
	.countdown {
		color: var(--fg);
	}
	.waiting {
		color: var(--muted);
	}
	.item-actions {
		display: flex;
		flex: none;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-xs);
	}
	.item-actions select {
		max-width: var(--field-wide);
	}
	/* Phones: the actions go under the call. */
	@media (max-width: 47.99rem) {
		.conversation {
			padding: var(--space-md);
		}
		.item {
			flex-wrap: wrap;
		}
		.item-actions {
			flex-basis: 100%;
			padding-left: calc(var(--target) + var(--space-md));
		}
		.gone .item-actions {
			flex-basis: auto;
			padding-left: 0;
		}
	}
</style>
