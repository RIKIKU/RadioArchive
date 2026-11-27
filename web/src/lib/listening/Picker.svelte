<script lang="ts">
	// The talkgroup search (SPEC.md R42, R83; ADR 0048): RadioArchive's own,
	// from ArchiveUx's tokens. It finds single talkgroups by name, label, ID
	// or tag; each result is a chip (POC, Chip.svelte): a tap switches it on
	// or off, a long press or its star makes it priority. Whole groups and
	// tags are the tiles' (Tiles.svelte). About 1,000 talkgroups, all in the
	// browser, with no paging: the search runs here, as you type.
	import Chip from './Chip.svelte';
	import { keyOf, search, type Index, type Talkgroup } from './set.ts';
	import type { ListeningState } from './state.svelte.ts';

	let { ix, listening }: { ix: Index; listening: ListeningState } = $props();

	let query = $state('');
	const uid = $props.id();

	const found = $derived(query.trim() ? search(ix.catalogue.talkgroups, query) : []);
	const on = $derived(new Set(listening.saved.map(keyOf)));
	const prio = $derived(new Set(listening.saved.filter((e) => e.priority).map(keyOf)));

	const count = (n: number) => `${n} talkgroup${n === 1 ? '' : 's'}`;

	function toggle(t: Talkgroup) {
		if (on.has(keyOf(t))) listening.remove(t);
		else listening.add(t);
	}
</script>

<div class="picker">
	<div class="field find">
		<label for="{uid}-q">Search talkgroups</label>
		<input id="{uid}-q" type="search" bind:value={query} autocomplete="off" aria-describedby="{uid}-hint" />
		<p class="field-hint" id="{uid}-hint">By name, label, ID or tag. Tap one to switch it on or off; hold it, or tap its star, for priority.</p>
	</div>

	{#if query.trim()}
		<p class="status" role="status">{found.length ? `${count(found.length)} found` : 'No talkgroups match.'}</p>
		{#if found.length}
			<ul class="chips" aria-label="Search results">
				{#each found as t (keyOf(t))}
					<li>
						<Chip
							talkgroup={t}
							on={on.has(keyOf(t))}
							priority={prio.has(keyOf(t))}
							note={listening.inSession(t) ? 'Added for this session' : undefined}
							ontoggle={() => toggle(t)}
							onpriority={() => listening.togglePriority(t)}
						/>
					</li>
				{/each}
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
	.chips {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(min(100%, var(--field-min)), 1fr));
		gap: var(--space-xs);
		max-width: var(--measure);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.chips li {
		display: flex;
		flex-direction: column;
		min-width: 0;
	}
</style>
