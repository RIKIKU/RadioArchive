<script lang="ts">
	// The Talkgroups page (SPEC.md P1, R17, R21-R24, R30, R31, R34, R50,
	// R80, R83, R100; A13, A23, A25, A36; ADR 0003, 0006, 0026, 0031), an
	// admin page:
	//
	// - every talkgroup of every system in one table, from one request
	//   (GET /admin/talkgroups, no paging), searched across ID, label and
	//   name and filtered by system, group, tag, state, unreviewed and "not
	//   heard in N days" in the browser as it's typed, with no further
	//   request (A23); every column sorts (ArchiveUx's DataTable);
	// - editing one talkgroup at a time, in a panel above the table that
	//   names it, its row marked (TalkgroupEditor); no bulk editing (R50);
	// - each row's state changed from the row (More actions), at once for
	//   listeners (R30, R31: the catalogue's generation, ADR 0006); ignoring
	//   and making public ask first, in a dialog, since the row's question
	//   would otherwise be off-screen in a long table;
	// - its recordings played from the row in the page's own player, hidden
	//   and ignored talkgroups' too (R34, A13; TalkgroupCalls);
	// - the talkgroups' rejected calls beside it (R17), and the unreviewed
	//   talkgroups auto-create made (R21), a click away.
	import { onDestroy, onMount, tick } from 'svelte';
	import { Banner, Confirm, DataTable, MoreActions, StateBadge, sortRows } from 'archiveux/components';
	import AdminPage from '#lib/admin/AdminPage.svelte';
	import TalkgroupCalls from '#lib/admin/TalkgroupCalls.svelte';
	import TalkgroupEditor from '#lib/admin/TalkgroupEditor.svelte';
	import { admin, agoOf, deploymentZone, type Access, type AdminError } from '#lib/admin/api.ts';
	import {
		asksFirst,
		filterRows,
		keyOf,
		namesIn,
		noFilters,
		quietDaysOf,
		stateWords,
		states,
		titleOf,
		type Label,
		type State,
		type System,
		type Table,
		type TableRow,
		type TalkgroupRejection
	} from '#lib/admin/talkgroups.ts';

	type Column = { key: string; label: string; sortable?: boolean; firstDir?: 'ascending' | 'descending'; align?: 'start' | 'end'; primary?: boolean; custom?: boolean; value?: (r: TableRow) => unknown; text?: (r: TableRow) => string };
	type Sort = { key: string; dir: 'ascending' | 'descending' };

	let access: Access = $state('checking');
	let rows: TableRow[] = $state([]);
	let systems: System[] = $state([]);
	let tags: Label[] = $state([]);
	let groups: Label[] = $state([]);
	let rejected: TalkgroupRejection[] = $state([]);
	let timeZone = $state('UTC');
	let now = $state(Date.now());

	let filters = $state(noFilters());
	let sort: Sort | null = $state(null);
	/** The one talkgroup open above the table: being edited, or its recordings. */
	let open: { kind: 'edit' | 'calls'; key: string } | undefined = $state();
	/** A change of state from a row, waiting for the admin's answer. */
	let asking: { row: TableRow; to: State } | undefined = $state();
	let status = $state('');
	let problem = $state('');
	/** Where the focus goes back to when the panel closes. */
	let opener: (() => HTMLElement | null) | undefined;

	async function loadLabels() {
		const [t, g] = await Promise.all([admin<Label[]>('GET', '/tags'), admin<Label[]>('GET', '/groups')]);
		if (t.access === 'open' && t.body) tags = t.body;
		if (g.access === 'open' && g.body) groups = g.body;
	}

	async function load() {
		const [t, s, j] = await Promise.all([admin<Table>('GET', '/talkgroups'), admin<System[]>('GET', '/systems'), admin<{ talkgroups: TalkgroupRejection[] }>('GET', '/rejections'), loadLabels()]);
		now = Date.now();
		access = t.access;
		if (t.access !== 'open' || !t.body) return;
		rows = t.body.talkgroups;
		if (s.access === 'open' && s.body) systems = s.body;
		if (j.access === 'open' && j.body) rejected = j.body.talkgroups;
	}

	let clock: ReturnType<typeof setInterval> | undefined;
	onMount(() => {
		void deploymentZone().then((z) => (timeZone = z));
		void load();
		// "Not heard in N days" and "4 min ago" move with the time; no request.
		clock = setInterval(() => (now = Date.now()), 60_000);
	});
	onDestroy(() => clearInterval(clock));

	const shown = $derived(filterRows(rows, filters, now));
	const systemIDs = $derived([...new Set(rows.map((r) => r.system_id))]);
	const several = $derived(systemIDs.length > 1 || systems.length > 1);
	const systemOf = (id: number) => systems.find((s) => s.id === id);
	const tagNames = $derived(namesIn(rows, 'tag'));
	const groupNames = $derived(namesIn(rows, 'groups'));
	const unreviewed = $derived(rows.filter((r) => r.unreviewed).length);
	// Quiet talkgroups (R114; ADR 0054): every row is here, quiet ones badged.
	const quietCount = $derived(rows.filter((r) => r.quiet).length);
	const quietTyped = $derived(filters.quietDays.trim() !== '' && quietDaysOf(filters.quietDays) === undefined);
	const filtering = $derived(JSON.stringify(filters) !== JSON.stringify(noFilters()));
	const openRow = $derived(open ? rows.find((r) => keyOf(r) === open!.key) : undefined);

	const n = (x: number) => x.toLocaleString('en-AU');
	const plural = (x: number, one: string, many = `${one}s`) => `${n(x)} ${x === 1 ? one : many}`;
	const stateOrder = (s: State) => states.indexOf(s);

	const columns: Column[] = $derived([
		{ key: 'id', label: 'TG', sortable: true, primary: true, custom: true, value: (r) => r.id },
		...(several ? [{ key: 'system', label: 'System', sortable: true, value: (r: TableRow) => systemOf(r.system_id)?.label ?? String(r.system_id) }] : []),
		{ key: 'label', label: 'Label', sortable: true, custom: true },
		{ key: 'name', label: 'Name', sortable: true },
		{ key: 'tag', label: 'Tag', sortable: true, custom: true },
		{ key: 'groups', label: 'Groups', sortable: true, custom: true, value: (r) => r.groups.join(', ') },
		{ key: 'state', label: 'State', sortable: true, custom: true, value: (r) => stateOrder(r.state) * 2 + (r.unreviewed ? 1 : 0) },
		{ key: 'last_heard_at', label: 'Last heard', sortable: true, firstDir: 'descending', value: (r) => (r.last_heard_at ? new Date(r.last_heard_at).getTime() : null), text: (r) => (r.last_heard_at ? agoOf(r.last_heard_at, now) : 'Never') },
		{ key: 'calls_30_days', label: 'Calls (30 days)', sortable: true, firstDir: 'descending', align: 'end', text: (r) => n(r.calls_30_days) }
	]);

	// Every row is searched, filtered and sorted here, over all of them; the
	// table draws the first hundred, and more when asked at its foot, since a
	// thousand rows' controls take seconds to draw on a slow machine (ADR
	// 0031, "Speed").
	const BATCH = 100;
	let limit = $state(BATCH);
	const ordered = $derived(sortRows(shown, columns, sort));
	const drawn = $derived(ordered.slice(0, limit));
	const left = $derived(ordered.length - drawn.length);

	const actionWords: Record<State, string> ={ public: 'Make public…', hidden: 'Hide', ignored: 'Ignore…' };

	function moreButton(t: TableRow): HTMLElement | null {
		return document.querySelector<HTMLElement>(`button[aria-label="${CSS.escape(`More actions: ${titleOf(t)}`)}"]`);
	}

	async function show(kind: 'edit' | 'calls', t: TableRow, back: () => HTMLElement | null) {
		status = '';
		problem = '';
		opener = back;
		open = { kind, key: keyOf(t) };
	}

	async function close(said = '') {
		open = undefined;
		if (said) status = said;
		await tick();
		const el = opener?.();
		opener = undefined;
		if (el && el.isConnected) el.focus();
		else document.getElementById('tg-q')?.focus();
	}

	function merge(t: Partial<TableRow>) {
		if (t.id === undefined) return;
		const k = keyOf(t as TableRow);
		rows = rows.map((r) => (keyOf(r) === k ? { ...r, ...t } : r));
	}

	async function saved(t: Partial<TableRow>, said: string) {
		const before = openRow;
		merge(t);
		if (before && t.id !== undefined && (t.tag !== before.tag || JSON.stringify(t.groups) !== JSON.stringify(before.groups))) void loadLabels();
		await close(said);
	}

	function changeState(t: TableRow, to: State) {
		status = '';
		problem = '';
		if (asksFirst(t.state, to)) {
			asking = { row: t, to };
			return;
		}
		void sendState(t, to);
	}

	async function sendState(t: TableRow, to: State) {
		const a = await admin<TableRow | AdminError>('PATCH', `/systems/${t.system_id}/talkgroups/${t.id}`, { state: to });
		if (a.access === 'sign-in' || a.access === 'not-admin') {
			access = a.access;
			return;
		}
		if (a.access !== 'open' || !a.body || 'error' in a.body) {
			const e = a.body && 'error' in a.body ? a.body : undefined;
			const msg = e?.error === 'tag_required' ? `${titleOf(t)} needs a tag first: edit it to give it one.` : (e?.message ?? `Couldn't change ${titleOf(t)} just now. Nothing changed. Try again in a minute.`);
			if (asking) throw new Error(msg);
			problem = msg;
			return;
		}
		asking = undefined;
		merge(a.body);
		status = `${titleOf(a.body)} is ${a.body.state} now.`;
	}

	const confirmWords = (t: TableRow, to: State) =>
		to === 'ignored'
			? { title: `Ignore ${titleOf(t)}?`, confirm: `Ignore ${titleOf(t)}`, words: "From now on its calls are refused, not recorded, and can't be got back. The calls it already has stay hidden until retention deletes them." }
			: { title: `Make ${titleOf(t)} public?`, confirm: `Make ${titleOf(t)} public`, words: 'Its recordings, and the calls patched with it, can be heard by anyone at once, live and in recent calls.' };
</script>

<AdminPage title="Talkgroups" {access}>
	<section class="rejected" aria-labelledby="rejected-heading">
		<h2 id="rejected-heading">Rejected calls</h2>
		{#if rejected.length}
			<ul aria-labelledby="rejected-heading">
				{#each rejected as j (`${j.system_id}/${j.talkgroup}/${j.reason}`)}<li>{several ? `${systemOf(j.system_id)?.label ?? j.system_id}: ` : ''}{j.message}</li>{/each}
			</ul>
		{:else}
			<p class="meta">No calls have been rejected for a talkgroup.</p>
		{/if}
	</section>

	{#if unreviewed && !filters.unreviewed}
		<Banner tone="info">
			<p>
				{unreviewed === 1 ? '1 talkgroup made by auto-create is' : `${n(unreviewed)} talkgroups made by auto-create are`} not reviewed yet.
				<button type="button" class="link" onclick={() => (filters.unreviewed = true)}>Show {unreviewed === 1 ? 'it' : 'them'}</button>
			</p>
		</Banner>
	{/if}

	<form class="filters" role="search" aria-label="Talkgroups" novalidate onsubmit={(e) => e.preventDefault()}>
		<div class="field search">
			<label for="tg-q">Search</label>
			<input id="tg-q" type="search" autocomplete="off" bind:value={filters.q} aria-describedby="tg-q-hint" />
			<p class="field-hint" id="tg-q-hint">ID, label or name.</p>
		</div>
		{#if several}
			<div class="field">
				<label for="f-system">System</label>
				<select id="f-system" bind:value={filters.system}>
					<option value="">Any</option>
					{#each systemIDs as id (id)}<option value={String(id)}>{systemOf(id)?.label ?? id}</option>{/each}
				</select>
			</div>
		{/if}
		<div class="field">
			<label for="f-group">Group</label>
			<select id="f-group" bind:value={filters.group}>
				<option value="">Any</option>
				{#each groupNames as g (g)}<option value={g}>{g}</option>{/each}
			</select>
		</div>
		<div class="field">
			<label for="f-tag">Tag</label>
			<select id="f-tag" bind:value={filters.tag}>
				<option value="">Any</option>
				{#each tagNames as g (g)}<option value={g}>{g}</option>{/each}
			</select>
		</div>
		<div class="field">
			<label for="f-state">State</label>
			<select id="f-state" bind:value={filters.state}>
				<option value="">Any</option>
				{#each states as s (s)}<option value={s}>{stateWords[s]}</option>{/each}
			</select>
		</div>
		<div class="field">
			<label for="f-quiet">Not heard in (days)</label>
			<input id="f-quiet" autocomplete="off" inputmode="numeric" bind:value={filters.quietDays} aria-invalid={quietTyped ? 'true' : undefined} aria-describedby={quietTyped ? 'f-quiet-error' : undefined} />
			{#if quietTyped}<p class="field-error" id="f-quiet-error">Enter whole days, such as 30.</p>{/if}
		</div>
		<div class="field check">
			<label><input type="checkbox" bind:checked={filters.unreviewed} /> Unreviewed only</label>
		</div>
		{#if quietCount}
			<div class="field check">
				<label><input type="checkbox" bind:checked={filters.quiet} /> Quiet only</label>
			</div>
		{/if}
		{#if filtering}
			<div class="field check">
				<button type="button" class="secondary" onclick={() => (filters = noFilters())}>Clear search and filters</button>
			</div>
		{/if}
	</form>

	<p class="count" role="status">{rows.length ? `Showing ${n(shown.length)} of ${plural(rows.length, 'talkgroup')}.` : ''}</p>
	<p class="status" role="status">{status}</p>
	{#if problem}<Banner tone="error" role="alert"><p>{problem}</p></Banner>{/if}

	{#if open && openRow}
		{#key open.kind + open.key}
			{#if open.kind === 'edit'}
				<TalkgroupEditor
					talkgroup={openRow}
					{tags}
					{groups}
					system={systemOf(openRow.system_id)}
					onsaved={saved}
					oncancel={() => close(`Nothing changed on ${titleOf(openRow!)}.`)}
					onaccess={(a) => (access = a)}
				/>
			{:else}
				<TalkgroupCalls talkgroup={openRow} {timeZone} onclose={() => close()} />
			{/if}
		{/key}
	{/if}

	<!-- The last column's More actions tooltips reach past the page's edge in a wide look: clipped, never a sideways scroll, and never clipping the lists below. -->
	<div class="table-edge">
	<DataTable
		caption="Talkgroups"
		{columns}
		rows={drawn}
		key={keyOf}
		bind:sort
		sortRows={false}
		empty={rows.length ? 'No talkgroups match the search and filters.' : 'No talkgroups yet. Talkgroups come from the import, auto-create or the admin API.'}
		rowProps={(r: TableRow) => ({ 'data-open': open?.key === keyOf(r) ? open.kind : undefined })}
	>
		{#snippet cell(t: TableRow, c: Column)}
			{#if c.key === 'id'}
				{t.id}
			{:else if c.key === 'label' || c.key === 'tag' || c.key === 'groups'}
				<span class="words" class:groups={c.key === 'groups'}>{c.key === 'groups' ? t.groups.join(', ') : c.key === 'label' ? t.label : (t.tag ?? '')}</span>
			{:else if c.key === 'state'}
				<span class="badges">
					<StateBadge state={t.state === 'public' ? 'ok' : 'neutral'} text={stateWords[t.state]} />
					{#if t.unreviewed}<StateBadge state="warning" text="Unreviewed" />{/if}
					{#if t.quiet && !t.always_show}<span title="Not heard in {t.not_heard_days ?? 0} days: listeners' lists leave it out until its next call"><StateBadge state="neutral" text="Quiet" /></span>{/if}
				</span>
			{/if}
		{/snippet}
		{#snippet actions(t: TableRow)}
			<button type="button" class="secondary" aria-label="Edit {titleOf(t)}" onclick={(e) => {
					const b = e.currentTarget;
					show('edit', t, () => b);
				}}>Edit</button
			>
			<MoreActions label="More actions: {titleOf(t)}">
				<button type="button" onclick={() => show('calls', t, () => moreButton(t))}>Recordings</button>
				<hr />
				{#each states.filter((s) => s !== t.state) as s (s)}
					<button type="button" class:danger={s === 'ignored'} onclick={() => changeState(t, s)}>{actionWords[s]}</button>
				{/each}
			</MoreActions>
		{/snippet}
	</DataTable>
	</div>
	{#if left > 0}
		<div class="more-rows">
			<p>The first {n(drawn.length)} of {n(ordered.length)} are drawn; the search, filters and sorting cover them all.</p>
			<button type="button" class="secondary" onclick={() => (limit += BATCH)}>Show {n(Math.min(BATCH, left))} more</button>
			<button type="button" class="secondary" onclick={() => (limit = ordered.length)}>Show all {n(ordered.length)}</button>
		</div>
	{/if}

	{#if asking}
		{@const q = confirmWords(asking.row, asking.to)}
		<Confirm title={q.title} confirmLabel={q.confirm} modal onconfirm={() => sendState(asking!.row, asking!.to)} oncancel={() => (asking = undefined)}>
			<p>{q.words}</p>
		</Confirm>
	{/if}

	<p class="meta">
		Last heard is the latest call heard on a talkgroup, whatever its state, and calls in the last 30 days count the calls still kept. A recording's time is {timeZone === 'UTC' ? 'UTC' : timeZone.split('/').pop()?.replaceAll('_', ' ')} time.
	</p>
</AdminPage>

<style>
	.meta {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.rejected ul {
		margin: 0 0 var(--space-md);
		padding-inline-start: var(--space-lg);
	}
	.filters {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		gap: 0 var(--space-md);
		margin-top: var(--space-md);
	}
	.filters .field {
		min-width: 0;
	}
	.filters .search {
		flex: 1 1 var(--field-wide);
		max-width: var(--field-wide);
	}
	.filters .search input {
		width: 100%;
	}
	.filters input:not([type='checkbox']):not([type='search']) {
		width: var(--field-narrow);
	}
	.filters .check {
		align-self: flex-end;
	}
	.filters .check label {
		display: flex;
		align-items: center;
		gap: var(--space-xs);
		min-height: var(--target);
	}
	.count,
	.status {
		margin: 0 0 var(--space-sm);
	}
	.status:empty,
	.count:empty {
		display: none;
	}
	.table-edge {
		overflow-x: clip;
	}
	.more-rows {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-sm);
		margin: var(--space-md) 0;
	}
	.more-rows p {
		margin: 0;
		color: var(--muted);
		font-size: var(--text-sm);
	}
	/* A label, a tag and the groups' names break only at their spaces
	   (#165): the table's cells may break anywhere, so a long name in a
	   crowded table came out as "Untagge|d". Each column is never narrower
	   than its longest word. Only a single word longer than a field (a label
	   or a tag) or a wide field (the groups) still breaks, so the table never
	   pushes the page wider (#113). Names stay as they were: they're long,
	   and their column gives up its room first. */
	.words {
		display: inline-block;
		max-inline-size: var(--field-narrow);
		overflow-wrap: break-word;
	}
	.words.groups {
		max-inline-size: var(--field-wide);
	}
	/* The room for them comes from the headers, which the table keeps on one
	   line: here "Last heard" and "Calls (30 days)" may take two, at their
	   spaces only, when the table is short of room. */
	.table-edge :global(thead th) {
		white-space: normal;
		overflow-wrap: normal;
	}
	.badges {
		display: inline-flex;
		flex-wrap: wrap;
		gap: var(--space-2xs);
	}
	:global(tr[data-open]) {
		outline: var(--border-width-strong) solid var(--accent);
		outline-offset: calc(-1 * var(--border-width-strong));
	}
	.link {
		min-height: 0;
		padding: 0;
		border: 0;
		background: none;
		color: inherit;
		font: inherit;
		font-weight: var(--weight-strong);
		text-decoration: underline;
	}
</style>
