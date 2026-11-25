<script lang="ts">
	// The Audit log page (SPEC.md R54, R84; ADR 0029, 0032), an admin page:
	// every admin change, newest first, from GET /api/v1/admin/audit, in
	// ArchiveUx's DataTable with the server doing the order and the paging
	// (sortRows={false}: When sorts by asking the server again, oldest or
	// newest first, and Load more asks for the next page with the cursor).
	//
	// - Filters: who (GET /audit/admins: everyone with an entry, the
	//   system's nightly prune included), the kind of thing, and the days
	//   (ArchiveUx's DateRange, on the deployment's clock). A row's History
	//   shows only that thing's changes. The filters are the page's URL, so
	//   a filtered view can be linked (audit.ts).
	// - Each change in words: "Label DISP N → DISP North", "State public →
	//   hidden", "Bench / ex attached to EX" (audit.ts's linesOf).
	// - Reading it adds nothing to it (R54).
	import { onMount, tick } from 'svelte';
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { Banner, DataTable, DateRange } from 'archiveux/components';
	import AdminPage from '#lib/admin/AdminPage.svelte';
	import { admin, deploymentZone, whenOf, type Access } from '#lib/admin/api.ts';
	import {
		kindOf,
		kinds,
		linesOf,
		noFilters,
		readFilters,
		requestOf,
		viaOf,
		whoOf,
		writeFilters,
		type AuditEntry,
		type AuditFilters,
		type AuditPage,
		type AuditWho
	} from '#lib/admin/audit.ts';

	const PAGE = 50;

	type Sort = { key: string; dir: 'ascending' | 'descending' };

	let access: Access = $state('checking');
	let timeZone = $state('UTC');
	let filters: AuditFilters = $state(noFilters());
	let entries: AuditEntry[] = $state([]);
	let next: string | null = $state(null);
	let admins: AuditWho[] = $state([]);
	let keptDays: number | undefined = $state();
	let loaded = $state(false);
	let loading = $state(false);
	let failed = $state(false);
	let sort: Sort = $state({ key: 'at', dir: 'descending' });
	/** The name of the thing History shows, once an entry has said it. */
	let thingLabel = $state('');
	/** Each new list's number: an answer to an older one is dropped. */
	let generation = 0;

	const columns = [
		{ key: 'at', label: 'When', sortable: true, firstDir: 'descending' as const, primary: true, custom: true },
		{ key: 'who', label: 'Who', custom: true },
		{ key: 'via', label: 'How', text: (e: AuditEntry) => viaOf(e.via) },
		{ key: 'thing', label: 'What', custom: true },
		{ key: 'changes', label: 'Change', custom: true }
	];

	async function load(more = false) {
		const g = more ? generation : ++generation;
		loading = true;
		const a = await admin<AuditPage>('GET', requestOf(filters, timeZone, PAGE, more ? (next ?? undefined) : undefined));
		if (g !== generation) return;
		loading = false;
		if (!loaded || a.access === 'sign-in' || a.access === 'not-admin') access = a.access;
		if (a.access !== 'open' || !a.body) {
			failed = access === 'open';
			return;
		}
		failed = false;
		loaded = true;
		entries = more ? [...entries, ...a.body.entries] : a.body.entries;
		next = a.body.next;
		if (filters.thing) thingLabel = entries.find((e) => e.thing === filters.thing)?.label ?? thingLabel;
	}

	async function readAdmins() {
		const a = await admin<AuditWho[]>('GET', '/audit/admins');
		if (a.access === 'open' && a.body) admins = a.body;
	}

	async function readKept() {
		const a = await admin<{ days: number }>('GET', '/audit/retention');
		if (a.access === 'open' && a.body) keptDays = a.body.days;
	}

	/** Shows the list for the filters as they are now, and puts them in the URL. */
	function apply() {
		replaceState(page.url.pathname + writeFilters(filters), page.state);
		sort = { key: 'at', dir: filters.oldest ? 'ascending' : 'descending' };
		void load();
	}

	/** A row's History: that thing's changes only. The focus goes to the way back, as the row may go. */
	async function only(e: AuditEntry) {
		filters.kind = e.kind;
		filters.thing = e.thing;
		thingLabel = e.label;
		apply();
		await tick();
		document.getElementById('show-all')?.focus();
	}

	async function showAll() {
		filters.thing = '';
		thingLabel = '';
		apply();
		await tick();
		document.getElementById('kind')?.focus();
	}

	onMount(() => {
		filters = readFilters(page.url.searchParams);
		sort = { key: 'at', dir: filters.oldest ? 'ascending' : 'descending' };
		void deploymentZone().then((z) => {
			timeZone = z;
			void load();
		});
		void readAdmins();
		void readKept();
	});

	const filtered = $derived(!!(filters.who || filters.kind || filters.from));
	const whoChoices = $derived(filters.who && !admins.some((w) => w.id === filters.who) ? [...admins, { id: filters.who, name: '', email: '' }] : admins);
	const plural = (n: number, one: string, many = `${one}s`) => `${n.toLocaleString('en-AU')} ${n === 1 ? one : many}`;
	const shown = $derived(
		!loaded
			? ''
			: entries.length === 0
				? ''
				: `${plural(entries.length, 'change')} shown, ${filters.oldest ? 'oldest' : 'newest'} first${next ? '; there are more.' : '.'}`
	);
</script>

<AdminPage title="Audit log" {access}>
	<p class="intro">
		Every admin change: who made it, how, when, and what changed. Reading it, and playing recordings, add nothing to it.
		{#if keptDays}Entries are kept for {plural(keptDays, 'day')}, then deleted at the nightly run; <a href="/admin/settings">Settings</a> changes that.{/if}
	</p>

	<form class="filters" aria-label="Filter the audit log" onsubmit={(e) => e.preventDefault()}>
		<div class="field">
			<label for="who">Who</label>
			<select id="who" bind:value={filters.who} onchange={apply}>
				<option value="">Anyone</option>
				{#each whoChoices as w (w.id)}
					<option value={w.id}>{whoOf(w)}{w.name && w.email ? ` (${w.email})` : ''}</option>
				{/each}
			</select>
		</div>
		<div class="field">
			<label for="kind">Kind of thing</label>
			<select
				id="kind"
				bind:value={filters.kind}
				onchange={() => {
					filters.thing = '';
					thingLabel = '';
					apply();
				}}
			>
				<option value="">Any kind</option>
				{#each kinds as k (k.value)}<option value={k.value}>{k.label}</option>{/each}
			</select>
		</div>
		<div class="field dates" role="group" aria-labelledby="dates-label" aria-describedby="dates-hint">
			<span class="label" id="dates-label">Dates</span>
			<div class="date-range">
				<DateRange
					from={filters.from}
					to={filters.to}
					{timeZone}
					onchange={(from, to) => {
						filters.from = from;
						filters.to = to;
						apply();
					}}
				/>
				{#if filters.from}
					<button
						type="button"
						class="ghost"
						onclick={() => {
							filters.from = '';
							filters.to = '';
							apply();
						}}>Any time</button
					>
				{/if}
			</div>
			<p class="field-hint" id="dates-hint">Days on the deployment's clock ({timeZone}).</p>
		</div>
	</form>

	{#if filters.thing}
		<p class="only">
			Only changes to <strong>{kindOf(filters.kind)} {thingLabel || filters.thing}</strong>
			<button type="button" class="secondary" id="show-all" onclick={showAll}>Show every {kindOf(filters.kind).toLowerCase()}</button>
		</p>
	{/if}

	{#if failed}
		<Banner tone="error" role="alert"><p>Couldn't load the audit log just now. Try again in a minute.</p></Banner>
	{/if}

	{#if loaded}
		<DataTable
			caption="Audit log"
			{columns}
			rows={entries}
			key={(e: AuditEntry) => e.id}
			bind:sort
			sortRows={false}
			onsort={(s) => {
				filters.oldest = s.dir === 'ascending';
				apply();
			}}
			empty={filtered || filters.thing ? 'No changes match these filters.' : 'No changes yet.'}
		>
			{#snippet cell(e: AuditEntry, c: { key: string })}
				{#if c.key === 'at'}
					<time datetime={e.at}>{whenOf(e.at, timeZone)}</time>
				{:else if c.key === 'who'}
					<span class="who">{whoOf(e.who)}</span>
					{#if e.who.email && e.who.name}<span class="sub">{e.who.email}</span>{/if}
				{:else if c.key === 'thing'}
					<span class="sub">{kindOf(e.kind)}</span>
					<span class="thing">{e.label || kindOf(e.kind)}</span>
				{:else if c.key === 'changes'}
					<ul class="lines">
						{#each linesOf(e, timeZone) as line, i (i)}
							<li>{#each line as p, j (j)}{#if 'code' in p}<code>{p.code}</code>{:else}{p.text}{/if}{/each}</li>
						{/each}
					</ul>
				{/if}
			{/snippet}
			{#snippet actions(e: AuditEntry)}
				{#if e.thing && filters.thing !== e.thing}
					<button type="button" class="secondary" aria-label="History of {kindOf(e.kind).toLowerCase()} {e.label || e.thing}" onclick={() => only(e)}>History</button>
				{/if}
			{/snippet}
		</DataTable>
		<p class="status" role="status">{shown}</p>
		{#if next}
			<button type="button" class="secondary" disabled={loading} onclick={() => load(true)}>Load more</button>
		{/if}
	{:else if !failed}
		<p class="meta" role="status">Loading…</p>
	{/if}
</AdminPage>

<style>
	.intro {
		max-width: var(--measure);
	}
	.meta,
	.sub {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.sub,
	.who,
	.thing {
		display: block;
	}
	.filters {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		gap: 0 var(--space-lg);
		margin: var(--space-lg) 0 var(--space-sm);
	}
	.filters .field {
		flex: 1 1 var(--field-narrow);
		max-width: var(--field-wide);
		min-width: 0;
	}
	.filters select {
		width: 100%;
	}
	.label {
		display: block;
	}
	.date-range {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-xs);
	}
	.only {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-sm);
	}
	.lines {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.lines code {
		overflow-wrap: anywhere;
	}
	.status {
		margin: var(--space-sm) 0;
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.status:empty {
		display: none;
	}
</style>
