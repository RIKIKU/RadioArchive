<script lang="ts">
	// The Listen page's one talkgroup search (SPEC.md R42, R83, R112; ADR
	// 0048, 0051): RadioArchive's own, from ArchiveUx's tokens, under the
	// box and above the tiles. It finds every talkgroup, on or off, by
	// number, name, label or tag, the one whose number is the query first
	// (set.ts's search). Each result is a chip (Chip.svelte): a tap
	// switches it on or off, a long press or its star flips priority. Whole
	// groups and tags are the tiles' (Tiles.svelte). About 1,000 talkgroups,
	// all in the browser, with no paging: the search runs here, as you type.
	// The page hides the talkgroups on while query has text (bound).
	//
	// Quiet talkgroups (R114; ADR 0054) are left out of the search, apart
	// from one whose number, name or label is exactly the query, unless
	// "Include quiet talkgroups" is ticked. Either way a quiet one's chip
	// says how long it hasn't been heard.
	import Chip from './Chip.svelte';
	import { keyOf, quietNote, searchShown, type Index, type Talkgroup } from './set.ts';
	import type { ListeningState } from './state.svelte.ts';

	let { ix, listening, query = $bindable('') }: { ix: Index; listening: ListeningState; query?: string } = $props();

	const uid = $props.id();

	let includeQuiet = $state(false);
	const found = $derived(query.trim() ? searchShown(ix, query, includeQuiet) : []);
	const days = $derived(ix.catalogue.quiet_window_days ?? 7);
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
		<p class="field-hint" id="{uid}-hint">By number, name, label or tag, on or off. Tap one to switch it on or off; hold it, or tap its star, for priority.</p>
	</div>
	{#if ix.quiet}
		<label class="quiet">
			<input type="checkbox" bind:checked={includeQuiet} />
			<span>Include quiet talkgroups<span class="quiet-hint">{ix.quiet.toLocaleString('en-AU')} not heard in {days} day{days === 1 ? '' : 's'}</span></span>
		</label>
	{/if}

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
							note={listening.inSession(t) ? 'Added for this session' : quietNote(t)}
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
		margin-top: var(--space-lg);
		display: flex;
		flex-direction: column;
	}
	.find {
		max-width: var(--field-search);
	}
	.quiet {
		display: flex;
		align-items: flex-start;
		gap: var(--space-sm);
		min-height: var(--target);
		margin-bottom: var(--space-sm);
		cursor: pointer;
	}
	.quiet-hint {
		display: block;
		color: var(--muted);
		font-size: var(--text-sm);
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
