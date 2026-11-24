<script lang="ts">
	// The listening-set picker (SPEC.md R42, R83): RadioArchive's own, from
	// ArchiveUx's tokens and row pattern. Browse by group, switching on a
	// whole group (its talkgroups as they are now, not a link to the group)
	// or single talkgroups; or search by name, label, ID or tag. Grouped by
	// system when there are several (R15). About 1,000 talkgroups, all in
	// the browser, with no paging: the search runs here, as you type.
	import { search, type Index, type Talkgroup } from './set.ts';
	import type { ListeningState } from './state.svelte.ts';

	let { ix, listening }: { ix: Index; listening: ListeningState } = $props();

	let query = $state('');
	let open: Record<string, boolean> = $state({});
	const uid = $props.id();

	const found = $derived(query.trim() ? search(ix.catalogue.talkgroups, query) : []);

	interface Shelf {
		id: string;
		name: string;
		talkgroups: Talkgroup[];
	}
	interface Section {
		id: string;
		system?: string;
		shelves: Shelf[];
	}

	// Each system's groups, in the catalogue's order, and its talkgroups in
	// no group last. One system: one section with no heading.
	const sections: Section[] = $derived.by(() => {
		const systems = ix.catalogue.systems?.length ? ix.catalogue.systems : [undefined];
		return systems.map((s) => {
			const mine = ix.catalogue.talkgroups.filter((t) => s === undefined || t.system === s.id);
			const shelves: Shelf[] = ix.catalogue.groups
				.map((g, i) => ({ id: `${s?.id ?? 0}-g${i}`, name: g, talkgroups: mine.filter((t) => t.groups.includes(g)) }))
				.filter((g) => g.talkgroups.length);
			const loose = mine.filter((t) => !t.groups.length);
			if (loose.length) shelves.push({ id: `${s?.id ?? 0}-none`, name: 'Not in a group', talkgroups: loose });
			return { id: String(s?.id ?? 0), system: s ? s.name || s.label : undefined, shelves };
		});
	});

	const count = (n: number) => `${n} talkgroup${n === 1 ? '' : 's'}`;
	const allIn = (ts: Talkgroup[]) => ts.every((t) => listening.inSet(t));

	function toggle(t: Talkgroup, on: boolean) {
		if (on) listening.add(t);
		else listening.remove(t);
	}
</script>

{#snippet row(t: Talkgroup)}
	<li class="row">
		<label class="pick">
			<input type="checkbox" checked={listening.inSet(t)} onchange={(e) => toggle(t, e.currentTarget.checked)} />
			<span class="what">
				<span class="name">{t.name}</span>
				<span class="meta">
					{#if t.label && t.label !== t.name}<span>{t.label}</span>{/if}
					<span>{t.tag}</span>
					<span>TG {t.id}</span>
				</span>
			</span>
		</label>
		{#if listening.inSession(t)}<span class="note">Added for this session</span>{/if}
	</li>
{/snippet}

<div class="picker">
	<div class="field find">
		<label for="{uid}-q">Search talkgroups</label>
		<input id="{uid}-q" type="search" bind:value={query} autocomplete="off" aria-describedby="{uid}-hint" />
		<p class="field-hint" id="{uid}-hint">By name, label, ID or tag.</p>
	</div>

	{#if query.trim()}
		<p class="status" role="status">{found.length ? `${count(found.length)} found` : 'No talkgroups match.'}</p>
		{#if found.length}
			<ul class="rows" aria-label="Search results">
				{#each found as t (`${t.system ?? 0}.${t.id}`)}{@render row(t)}{/each}
			</ul>
		{/if}
	{:else}
		{#each sections as s (s.id)}
			{#if s.system}<h3 class="system">{s.system}</h3>{/if}
			<ul class="rows" aria-label={s.system ? `Groups in ${s.system}` : 'Groups'}>
				{#each s.shelves as g (g.id)}
					{@const opened = !!open[g.id]}
					{@const every = allIn(g.talkgroups)}
					<li class="group">
						<div class="head">
							<button
								type="button"
								class="ghost open"
								aria-expanded={opened}
								aria-controls="{uid}-{g.id}"
								onclick={() => (open = { ...open, [g.id]: !opened })}
							>
								<span class="name">{g.name}</span>
								<span class="meta">{count(g.talkgroups.length)}</span>
							</button>
							{#if every}
								<button type="button" class="secondary" aria-label="Remove all of {g.name}" onclick={() => listening.remove(...g.talkgroups)}>Remove all</button>
							{:else}
								<button type="button" class="secondary" aria-label="Add all of {g.name}" onclick={() => listening.add(...g.talkgroups)}>Add all</button>
							{/if}
						</div>
						<ul class="rows inner" id="{uid}-{g.id}" hidden={!opened} aria-label={g.name}>
							{#if opened}
								{#each g.talkgroups as t (`${t.system ?? 0}.${t.id}`)}{@render row(t)}{/each}
							{/if}
						</ul>
					</li>
				{/each}
			</ul>
		{/each}
	{/if}
</div>

<style>
	.picker {
		display: flex;
		flex-direction: column;
	}
	.find {
		max-width: var(--field-search);
	}
	.rows {
		list-style: none;
		margin: 0;
		padding: 0;
		border-top: var(--border-width) solid var(--border);
	}
	.rows[hidden] {
		display: none;
	}
	.inner {
		border-top: 0;
		padding-left: var(--row-pad-x);
	}
	.row,
	.group > .head {
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
	.inner > .row:last-child {
		border-bottom: 0;
	}
	.group {
		border-bottom: var(--border-width) solid var(--border);
	}
	.group > .head {
		border-bottom: 0;
		justify-content: space-between;
	}
	.pick {
		display: flex;
		align-items: center;
		gap: var(--space-md);
		flex: 1 1 auto;
		min-width: 0;
		min-height: var(--target);
		cursor: pointer;
	}
	.what {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: var(--space-2xs) var(--space-md);
		min-width: 0;
	}
	.name {
		font-weight: var(--weight-strong);
		overflow-wrap: anywhere;
	}
	.meta {
		display: inline-flex;
		flex-wrap: wrap;
		gap: var(--space-2xs) var(--space-md);
		color: var(--muted);
		font-family: var(--font-meta);
		font-size: var(--text-sm);
		font-weight: var(--weight-normal);
	}
	.open {
		flex: 1 1 auto;
		justify-content: flex-start;
		flex-wrap: wrap;
		gap: var(--space-2xs) var(--space-md);
		text-align: left;
	}
	.note {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.system {
		margin: var(--space-xl) 0 var(--space-sm);
		font-size: var(--text-lg);
	}
</style>
