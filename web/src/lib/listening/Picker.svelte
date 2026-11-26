<script lang="ts">
	// The talkgroup search (SPEC.md R42, R83; ADR 0048): RadioArchive's own,
	// from ArchiveUx's tokens and row pattern. It finds single talkgroups by
	// name, label, ID or tag and switches each on or off; whole groups and
	// tags are the tiles' (Tiles.svelte). About 1,000 talkgroups, all in the
	// browser, with no paging: the search runs here, as you type.
	import { search, type Index, type Talkgroup } from './set.ts';
	import type { ListeningState } from './state.svelte.ts';

	let { ix, listening }: { ix: Index; listening: ListeningState } = $props();

	let query = $state('');
	const uid = $props.id();

	const found = $derived(query.trim() ? search(ix.catalogue.talkgroups, query) : []);

	const count = (n: number) => `${n} talkgroup${n === 1 ? '' : 's'}`;

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
	.note {
		color: var(--muted);
		font-size: var(--text-sm);
	}
</style>
