<script lang="ts">
	// What differs between two listening sets (R41), under a prompt that asks
	// which one to keep: the account's or a share link's against this
	// browser's. Folded until opened, and never a list of the whole set: a
	// few names of each side, then how many more, so the prompt's question
	// and buttons stay at the top however big the sets are.
	import type { Comparison, Shown } from './set.ts';

	let { c, offered, current }: { c: Comparison; offered: string; current: string } = $props();

	/** How many names of each side are shown. */
	const SHOWN = 12;
	const n = (x: number) => x.toLocaleString('en-AU');
	const plural = (x: number, one: string) => `${n(x)} ${one}${x === 1 ? '' : 's'}`;
	const differs = $derived(c.onlyOffered.length + c.onlyCurrent.length + c.marks > 0);
</script>

{#snippet names(label: string, rows: Shown[])}
	{#if rows.length}
		<p class="side">{label} <span class="count">({n(rows.length)})</span></p>
		<ul class="names" aria-label={label}>
			{#each rows.slice(0, SHOWN) as r (`${r.talkgroup.system ?? 0}.${r.talkgroup.id}`)}
				<li>{r.talkgroup.name}{#if r.priority}<span class="mark">Priority</span>{/if}</li>
			{/each}
		</ul>
		{#if rows.length > SHOWN}<p class="more">and {n(rows.length - SHOWN)} more</p>{/if}
	{/if}
{/snippet}

{#if differs}
	<details class="differences">
		<summary>Show the differences</summary>
		<p>
			{n(c.both)} in both{#if c.marks}, {plural(c.marks, 'priority mark')} different{/if}.
		</p>
		{@render names(`Only in ${offered}`, c.onlyOffered)}
		{@render names(`Only in ${current}`, c.onlyCurrent)}
	</details>
{/if}

<style>
	.differences {
		margin-top: var(--space-md);
	}
	.differences summary {
		cursor: pointer;
		width: fit-content;
	}
	.differences p {
		margin: var(--space-sm) 0 0;
	}
	.side {
		font-weight: var(--weight-strong);
	}
	.count,
	.more {
		color: var(--muted);
	}
	.names {
		margin: var(--space-xs) 0 0;
		padding-left: var(--space-xl);
	}
	.mark {
		margin-left: var(--space-sm);
		color: var(--muted);
		font-size: var(--text-sm);
	}
</style>
