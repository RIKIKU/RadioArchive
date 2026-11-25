<script lang="ts">
	// The Units page (SPEC.md R26, R27, R50, R80, R83; A36; ADR 0019, 0031,
	// 0034), an admin page:
	//
	// - every unit of every system in one table (GET /systems, then
	//   GET /systems/{id}/units for each), searched across radio ID and alias
	//   and filtered by system in the browser as it's typed, with no further
	//   request; every column sorts (ArchiveUx's DataTable), and the table
	//   draws a hundred rows at a time, as the Talkgroups page does;
	// - adding an exact unit (5001) or a range (5100-5199) with its alias
	//   (PUT /systems/{id}/units/{unit}); editing a unit's alias in its row,
	//   one at a time; deleting one after asking, in a dialog;
	// - which alias a radio ID gets (ADR 0019: its own unit, else the
	//   narrowest range covering it, else none), as typed.
	import { onMount, tick } from 'svelte';
	import { Banner, Confirm, DataTable, MoreActions, sortRows } from 'archiveux/components';
	import AdminPage from '#lib/admin/AdminPage.svelte';
	import { admin, type Access, type AdminError } from '#lib/admin/api.ts';
	import type { System } from '#lib/admin/talkgroups.ts';
	import {
		aliasProblem,
		fieldOfError,
		filterUnits,
		keyOf,
		nameOf,
		noFilters,
		parseUnit,
		resolve,
		resolvedWords,
		sizeOf,
		titleOf,
		type Unit
	} from '#lib/admin/units.ts';

	type Column = { key: string; label: string; sortable?: boolean; firstDir?: 'ascending' | 'descending'; align?: 'start' | 'end'; primary?: boolean; custom?: boolean; value?: (r: Unit) => unknown; text?: (r: Unit) => string };
	type Sort = { key: string; dir: 'ascending' | 'descending' };

	let access: Access = $state('checking');
	let units: Unit[] = $state([]);
	let systems: System[] = $state([]);
	let loaded = $state(false);

	let filters = $state(noFilters());
	let sort: Sort | null = $state(null);
	let status = $state('');
	let problem = $state('');

	/** The add form, as typed. */
	let add = $state({ system: '', unit: '', alias: '' });
	let addProblems: Partial<Record<'system' | 'unit' | 'alias', string>> = $state({});
	let adding = $state(false);

	/** The one unit whose alias is being edited in its row, and the alias as typed. */
	let editing: { key: string; alias: string; problem: string } | undefined = $state();
	let saving = $state(false);

	/** A unit waiting to be deleted, once the admin says so. */
	let asking: Unit | undefined = $state();
	let askError = $state('');

	/** "Which alias does a radio get?", as typed. */
	let which = $state({ system: '', radio: '' });

	const order = (a: Unit, b: Unit) => a.system_id - b.system_id || a.from - b.from || a.to - b.to;

	async function load() {
		const s = await admin<System[]>('GET', '/systems');
		access = s.access;
		if (s.access !== 'open' || !s.body) return;
		systems = s.body;
		const first = String(systems[0]?.id ?? '');
		if (!add.system) add.system = first;
		if (!which.system) which.system = first;
		const all = await Promise.all(systems.map((x) => admin<Unit[]>('GET', `/systems/${x.id}/units`)));
		const bad = all.find((a) => a.access !== 'open' || !a.body);
		if (bad) {
			if (bad.access === 'sign-in' || bad.access === 'not-admin') access = bad.access;
			else problem = "Couldn't load every system's units just now. Try again in a minute.";
		}
		units = all.flatMap((a) => a.body ?? []).sort(order);
		loaded = true;
	}

	onMount(() => void load());

	const several = $derived(systems.length > 1);
	const systemOf = (id: number) => systems.find((s) => s.id === id);
	const labelOf = (id: number) => systemOf(id)?.label ?? String(id);
	const shown = $derived(filterUnits(units, filters));
	const filtering = $derived(JSON.stringify(filters) !== JSON.stringify(noFilters()));

	const n = (x: number) => x.toLocaleString('en-AU');
	const plural = (x: number, one: string, many = `${one}s`) => `${n(x)} ${x === 1 ? one : many}`;

	const columns: Column[] = $derived([
		{ key: 'unit', label: 'Unit', sortable: true, primary: true, value: (u) => u.from, text: (u) => nameOf(u) },
		...(several ? [{ key: 'system', label: 'System', sortable: true, value: (u: Unit) => labelOf(u.system_id) }] : []),
		{ key: 'radios', label: 'Radios', sortable: true, align: 'end', value: (u) => sizeOf(u), text: (u) => n(sizeOf(u)) },
		{ key: 'alias', label: 'Alias', sortable: true, custom: true, value: (u) => u.alias }
	]);

	// As on the Talkgroups page (ADR 0031, "Speed"): every row searched,
	// filtered and sorted, the first hundred drawn, more when asked.
	const BATCH = 100;
	let limit = $state(BATCH);
	const ordered = $derived(sortRows(shown, columns, sort));
	const drawn = $derived(ordered.slice(0, limit));
	const left = $derived(ordered.length - drawn.length);

	// Which alias a radio gets, as typed.
	const whichRadio = $derived(/^\s*[1-9]\d{0,15}\s*$/.test(which.radio) ? Number(which.radio.trim()) : undefined);
	const whichTyped = $derived(which.radio.trim() !== '' && whichRadio === undefined);
	const whichSystem = $derived(Number(which.system || systems[0]?.id));
	const whichAnswer = $derived(whichRadio === undefined ? '' : resolvedWords(resolve(units, whichSystem, whichRadio), whichRadio));

	function editButton(u: Unit): HTMLElement | null {
		return document.querySelector<HTMLElement>(`button[aria-label="${CSS.escape(`Edit the alias of ${nameOf(u)}${several ? ` in ${labelOf(u.system_id)}` : ''}`)}"]`);
	}
	function moreButton(u: Unit): HTMLElement | null {
		return document.querySelector<HTMLElement>(`button[aria-label="${CSS.escape(`More actions: ${whose(u)}`)}"]`);
	}
	const whose = (u: Unit) => (several ? `${titleOf(u)} in ${labelOf(u.system_id)}` : titleOf(u));

	async function focusAfter(el: () => HTMLElement | null) {
		await tick();
		const e = el();
		if (e && e.isConnected) e.focus();
		else document.getElementById('unit-q')?.focus();
	}

	function denied(a: { access: Access }): boolean {
		if (a.access === 'sign-in' || a.access === 'not-admin') {
			access = a.access;
			return true;
		}
		return false;
	}

	async function addUnit(e: SubmitEvent) {
		e.preventDefault();
		status = '';
		problem = '';
		const p: typeof addProblems = {};
		const sys = Number(add.system);
		const span = parseUnit(add.unit);
		const alias = add.alias.trim();
		if (!systemOf(sys)) p.system = 'Choose a system.';
		if (!add.unit.trim()) p.unit = 'Enter a radio ID, such as 5001, or a range, such as 5100-5199.';
		else if (!span) p.unit = 'Enter a radio ID from 1, such as 5001, or a range whose last ID is above its first, such as 5100-5199.';
		else if (units.some((u) => u.system_id === sys && u.from === span.from && u.to === span.to))
			p.unit = `${nameOf(span)} already has a unit${several ? ` in ${labelOf(sys)}` : ''}: edit its alias in the table.`;
		const ap = aliasProblem(alias);
		if (ap) p.alias = ap;
		addProblems = p;
		if (Object.keys(p).length) {
			await tick();
			document.getElementById(p.system ? 'add-system' : p.unit ? 'add-unit' : 'add-alias')?.focus();
			return;
		}
		adding = true;
		const a = await admin<Unit | AdminError>('PUT', `/systems/${sys}/units/${nameOf(span!)}`, { alias });
		adding = false;
		if (denied(a)) return;
		if (a.status >= 400 && a.status < 500 && a.body && 'error' in a.body) {
			const field = fieldOfError(a.body.error);
			if (field) {
				addProblems = { [field]: a.body.message };
				await tick();
				document.getElementById(`add-${field}`)?.focus();
			} else problem = a.body.message;
			return;
		}
		if (a.access !== 'open' || !a.body || 'error' in a.body) {
			problem = "Couldn't add the unit just now. Nothing changed. Try again in a minute.";
			return;
		}
		const u = a.body;
		units = [...units.filter((x) => keyOf(x) !== keyOf(u)), u].sort(order);
		add = { system: add.system, unit: '', alias: '' };
		status = `Added ${whose(u)}.`;
		document.getElementById('add-unit')?.focus();
	}

	async function startEdit(u: Unit) {
		status = '';
		problem = '';
		editing = { key: keyOf(u), alias: u.alias, problem: '' };
		await tick();
		document.getElementById('edit-alias')?.focus();
	}

	async function cancelEdit(u: Unit, said = `Nothing changed on ${whose(u)}.`) {
		editing = undefined;
		status = said;
		await focusAfter(() => editButton(u));
	}

	async function saveEdit(e: SubmitEvent, u: Unit) {
		e.preventDefault();
		if (!editing || saving) return;
		const alias = editing.alias.trim();
		const p = aliasProblem(alias);
		if (p) {
			editing.problem = p;
			document.getElementById('edit-alias')?.focus();
			return;
		}
		if (alias === u.alias) {
			await cancelEdit(u, `Nothing to change on ${whose(u)}.`);
			return;
		}
		saving = true;
		const a = await admin<Unit | AdminError>('PUT', `/systems/${u.system_id}/units/${nameOf(u)}`, { alias });
		saving = false;
		if (denied(a)) return;
		if (a.access !== 'open' || !a.body || 'error' in a.body) {
			editing.problem = a.body && 'error' in a.body ? a.body.message : "Couldn't save just now. Nothing changed. Try again in a minute.";
			document.getElementById('edit-alias')?.focus();
			return;
		}
		const saved = a.body;
		units = units.map((x) => (keyOf(x) === keyOf(saved) ? saved : x));
		editing = undefined;
		status = saved.alias ? `Saved: ${nameOf(saved)} is ${saved.alias} now.` : `Saved: ${nameOf(saved)} has no alias now.`;
		await focusAfter(() => editButton(saved));
	}

	function editKeys(ev: KeyboardEvent, u: Unit) {
		if (ev.key === 'Escape') {
			ev.preventDefault();
			void cancelEdit(u);
		}
	}

	async function remove(u: Unit) {
		askError = '';
		const a = await admin<AdminError>('DELETE', `/systems/${u.system_id}/units/${nameOf(u)}`);
		if (denied(a)) return;
		if (a.status === 204 || a.status === 404) {
			units = units.filter((x) => keyOf(x) !== keyOf(u));
			asking = undefined;
			status = `Deleted ${whose(u)}.`;
			await focusAfter(() => document.getElementById('unit-q'));
			return;
		}
		askError = a.body?.message ?? "Couldn't delete it just now. Nothing changed. Try again in a minute.";
	}

	const deleteWords = (u: Unit) =>
		u.from === u.to
			? `From now on ${u.from} shows as the alias of the narrowest range covering it, if one has an alias, or as ${u.from}. Calls already kept show it that way too: a call's units are named when it's shown.`
			: `From now on its ${n(sizeOf(u))} radios show by their own units' aliases, or another range's, or by their IDs. Calls already kept show them that way too: a call's units are named when it's shown.`;
</script>

<AdminPage title="Units" {access}>
	<p class="intro">
		A unit names a radio ID, or a range of them, with an alias listeners see in its place. A radio's own unit beats a range covering it, and of two ranges the narrower wins.
	</p>

	{#if loaded && systems.length === 0}
		<Banner tone="info"><p>There are no systems yet. Add one on the <a href="/admin/systems">Systems</a> page first: a unit is a radio of a system.</p></Banner>
	{:else}
		<div class="tools">
			<section class="panel" aria-labelledby="add-heading">
				<h2 id="add-heading">Add a unit</h2>
				<form novalidate onsubmit={addUnit}>
					{#if several}
						<div class="field">
							<label for="add-system">System <span class="required">required</span></label>
							<select id="add-system" required bind:value={add.system} aria-invalid={addProblems.system ? 'true' : undefined} aria-describedby={addProblems.system ? 'add-system-error' : undefined}>
								{#each systems as s (s.id)}<option value={String(s.id)}>{s.label}</option>{/each}
							</select>
							{#if addProblems.system}<p class="field-error" id="add-system-error">{addProblems.system}</p>{/if}
						</div>
					{/if}
					<div class="field">
						<label for="add-unit">Radio ID or range <span class="required">required</span></label>
						<input
							id="add-unit"
							autocomplete="off"
							required
							bind:value={add.unit}
							aria-invalid={addProblems.unit ? 'true' : undefined}
							aria-describedby={['add-unit-hint', addProblems.unit ? 'add-unit-error' : ''].filter(Boolean).join(' ')}
						/>
						<p class="field-hint" id="add-unit-hint">One radio, such as 5001, or a range, such as 5100-5199.</p>
						{#if addProblems.unit}<p class="field-error" id="add-unit-error">{addProblems.unit}</p>{/if}
					</div>
					<div class="field">
						<label for="add-alias">Alias</label>
						<input
							id="add-alias"
							autocomplete="off"
							bind:value={add.alias}
							aria-invalid={addProblems.alias ? 'true' : undefined}
							aria-describedby={['add-alias-hint', addProblems.alias ? 'add-alias-error' : ''].filter(Boolean).join(' ')}
						/>
						<p class="field-hint" id="add-alias-hint">Such as Engine 1. Left empty, its radios show by their IDs, even inside a range with an alias.</p>
						{#if addProblems.alias}<p class="field-error" id="add-alias-error">{addProblems.alias}</p>{/if}
					</div>
					<button type="submit" disabled={adding}>Add the unit</button>
				</form>
			</section>

			<section class="panel" aria-labelledby="which-heading">
				<h2 id="which-heading">Which alias does a radio get?</h2>
				<form novalidate onsubmit={(e) => e.preventDefault()}>
					{#if several}
						<div class="field">
							<label for="which-system">System</label>
							<select id="which-system" bind:value={which.system}>
								{#each systems as s (s.id)}<option value={String(s.id)}>{s.label}</option>{/each}
							</select>
						</div>
					{/if}
					<div class="field">
						<label for="which-radio">Radio ID</label>
						<input
							id="which-radio"
							autocomplete="off"
							inputmode="numeric"
							bind:value={which.radio}
							aria-invalid={whichTyped ? 'true' : undefined}
							aria-describedby={whichTyped ? 'which-radio-error' : undefined}
						/>
						{#if whichTyped}<p class="field-error" id="which-radio-error">Enter one radio ID, such as 5150.</p>{/if}
					</div>
				</form>
				<p class="answer" role="status">{whichAnswer}</p>
			</section>
		</div>

		<form class="filters" role="search" aria-label="Units" novalidate onsubmit={(e) => e.preventDefault()}>
			<div class="field search">
				<label for="unit-q">Search</label>
				<input id="unit-q" type="search" autocomplete="off" bind:value={filters.q} aria-describedby="unit-q-hint" />
				<p class="field-hint" id="unit-q-hint">Radio ID or alias. A radio ID finds the ranges covering it too.</p>
			</div>
			{#if several}
				<div class="field">
					<label for="f-system">System</label>
					<select id="f-system" bind:value={filters.system}>
						<option value="">Any</option>
						{#each systems as s (s.id)}<option value={String(s.id)}>{s.label}</option>{/each}
					</select>
				</div>
			{/if}
			{#if filtering}
				<div class="field check">
					<button type="button" class="secondary" onclick={() => (filters = noFilters())}>Clear search and filter</button>
				</div>
			{/if}
		</form>

		<p class="count" role="status">{loaded ? `Showing ${n(shown.length)} of ${plural(units.length, 'unit')}.` : ''}</p>
		<p class="status" role="status">{status}</p>
		{#if problem}<Banner tone="error" role="alert"><p>{problem}</p></Banner>{/if}

		<div class="table-edge">
			<DataTable
				caption="Units"
				{columns}
				rows={drawn}
				key={keyOf}
				bind:sort
				sortRows={false}
				empty={units.length ? 'No units match the search and filter.' : loaded ? 'No units yet. Add one above, or with the import.' : 'Loading…'}
				rowProps={(u: Unit) => ({ 'data-open': editing?.key === keyOf(u) ? 'edit' : undefined })}
			>
				{#snippet cell(u: Unit, c: Column)}
					{#if c.key === 'alias'}
						{#if editing?.key === keyOf(u)}
							<form id="unit-edit" class="inline-edit" novalidate onsubmit={(e) => saveEdit(e, u)}>
								<label class="visually-hidden" for="edit-alias">Alias of {nameOf(u)}</label>
								<input
									id="edit-alias"
									autocomplete="off"
									bind:value={editing.alias}
									onkeydown={(e) => editKeys(e, u)}
									aria-invalid={editing.problem ? 'true' : undefined}
									aria-describedby={editing.problem ? 'edit-alias-error' : 'edit-alias-hint'}
								/>
								{#if editing.problem}<span class="field-error" id="edit-alias-error">{editing.problem}</span>{:else}<span class="field-hint" id="edit-alias-hint">Empty for none.</span>{/if}
							</form>
						{:else if u.alias}{u.alias}{:else}<span class="none">No alias</span>{/if}
					{/if}
				{/snippet}
				{#snippet actions(u: Unit)}
					{#if editing?.key === keyOf(u)}
						<button type="submit" form="unit-edit" disabled={saving} aria-label="Save the alias of {nameOf(u)}">Save</button>
						<button type="button" class="secondary" onclick={() => cancelEdit(u)} aria-label="Cancel editing {nameOf(u)}">Cancel</button>
					{:else}
						<button type="button" class="secondary" aria-label="Edit the alias of {nameOf(u)}{several ? ` in ${labelOf(u.system_id)}` : ''}" onclick={() => startEdit(u)}>Edit</button>
						<MoreActions label="More actions: {whose(u)}">
							<button
								type="button"
								class="danger"
								onclick={() => {
									askError = '';
									asking = u;
								}}>Delete…</button
							>
						</MoreActions>
					{/if}
				{/snippet}
			</DataTable>
		</div>
		{#if left > 0}
			<div class="more-rows">
				<p>The first {n(drawn.length)} of {n(ordered.length)} are drawn; the search, filter and sorting cover them all.</p>
				<button type="button" class="secondary" onclick={() => (limit += BATCH)}>Show {n(Math.min(BATCH, left))} more</button>
				<button type="button" class="secondary" onclick={() => (limit = ordered.length)}>Show all {n(ordered.length)}</button>
			</div>
		{/if}
	{/if}

	{#if asking}
		{@const u = asking}
		<Confirm
			title="Delete {whose(u)}?"
			confirmLabel="Delete {titleOf(u)}"
			modal
			error={askError}
			onconfirm={() => remove(u)}
			oncancel={() => {
				asking = undefined;
				void focusAfter(() => moreButton(u));
			}}
		>
			<p>{deleteWords(u)}</p>
		</Confirm>
	{/if}

	<p class="meta">A change shows at once wherever listeners see a call's units, calls already kept included. Last heard isn't kept for units: a unit's calls are found from Recent calls, by its radio ID or range.</p>
</AdminPage>

<style>
	.intro {
		max-width: var(--measure);
	}
	.meta {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.tools {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(min(100%, var(--field-wide)), 1fr));
		gap: var(--space-md);
		margin: var(--space-md) 0;
	}
	.panel {
		padding: var(--space-md);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
		background: var(--surface);
	}
	.panel h2 {
		margin-top: 0;
	}
	.panel input,
	.panel select {
		width: 100%;
	}
	.answer {
		margin: 0;
		font-weight: var(--weight-strong);
	}
	.answer:empty {
		display: none;
	}
	.filters {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		gap: 0 var(--space-md);
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
	.filters .check {
		align-self: flex-end;
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
	.none {
		color: var(--muted);
	}
	.inline-edit {
		display: flex;
		flex-direction: column;
		gap: var(--space-2xs);
	}
	/* Room on its right for the box's focus ring inside the row's ring. */
	.inline-edit {
		min-width: 0;
		padding-inline-end: var(--space-sm);
	}
	.inline-edit input {
		box-sizing: border-box;
		width: 100%;
		min-width: 0;
		max-width: var(--field-wide);
	}
	.inline-edit .field-hint,
	.inline-edit .field-error {
		margin: 0;
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
	:global(tr[data-open]) {
		outline: var(--border-width-strong) solid var(--accent);
		outline-offset: calc(-1 * var(--border-width-strong));
	}
</style>
