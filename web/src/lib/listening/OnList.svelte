<script lang="ts">
	// The talkgroups on, as chips (SPEC.md R41, R43, R112; ADR 0051).
	// Priority talkgroups first, then all of them by agency (a group, per
	// system when there are several), each agency folded away until opened
	// and its chips drawn only then: with every talkgroup on there are over a
	// thousand. A talkgroup in several groups is under each. Finding one is
	// the page's one search (Picker.svelte), above the tiles.
	//
	// A chip tapped off stays where it was, shown off, until the page is
	// left, so a stray tap is undone by tapping again; one tapped back on
	// gets its priority mark back. A chip whose star is taken off stays in
	// Priority the same way.
	import { SvelteSet } from 'svelte/reactivity';
	import Chip from './Chip.svelte';
	import { keyOf, type Index, type Talkgroup } from './set.ts';
	import type { ListeningState } from './state.svelte.ts';

	let { ix, listening }: { ix: Index; listening: ListeningState } = $props();

	const uid = $props.id();

	// Turned off here; priority when turned off; unstarred here.
	const offHere = new SvelteSet<string>();
	const wasPriority = new SvelteSet<string>();
	const unstarred = new SvelteSet<string>();
	const open = new SvelteSet<string>();

	const on = $derived(new Set(listening.saved.map(keyOf)));
	const prio = $derived(new Set(listening.saved.filter((e) => e.priority).map(keyOf)));

	const shown = $derived(ix.catalogue.talkgroups.filter((t) => on.has(keyOf(t)) || offHere.has(keyOf(t))));
	const priorityShown = $derived(shown.filter((t) => prio.has(keyOf(t)) || unstarred.has(keyOf(t)) || wasPriority.has(keyOf(t))));

	interface Group {
		id: string;
		name: string;
		talkgroups: Talkgroup[];
	}

	const groups: Group[] = $derived.by(() => {
		const all = ix.catalogue.systems ?? [];
		// The system's name only when there are several (R15).
		const systems = all.length > 1 ? all : [undefined];
		const out: Group[] = [];
		for (const s of systems) {
			const sid = s?.id ?? 0;
			const sys = s ? ` (${s.name || s.label})` : '';
			const mine = shown.filter((t) => s === undefined || t.system === s.id);
			ix.catalogue.groups.forEach((g, i) => {
				const ts = mine.filter((t) => t.groups.includes(g));
				if (ts.length) out.push({ id: `${sid}-g${i}`, name: g + sys, talkgroups: ts });
			});
			const loose = mine.filter((t) => !t.groups.length);
			if (loose.length) out.push({ id: `${sid}-gnone`, name: 'Not in a group' + sys, talkgroups: loose });
		}
		return out;
	});

	const num = (n: number) => n.toLocaleString('en-AU');
	const onIn = (ts: Talkgroup[]) => ts.filter((t) => on.has(keyOf(t))).length;

	function toggle(t: Talkgroup) {
		const k = keyOf(t);
		if (on.has(k)) {
			if (prio.has(k)) wasPriority.add(k);
			offHere.add(k);
			listening.remove(t);
		} else {
			listening.add(t);
			if (wasPriority.has(k)) {
				listening.setPriority(t, true);
				wasPriority.delete(k);
			}
		}
	}

	function star(t: Talkgroup) {
		const k = keyOf(t);
		if (prio.has(k)) unstarred.add(k);
		wasPriority.delete(k);
		listening.togglePriority(t);
	}

	function flip(id: string) {
		if (open.has(id)) open.delete(id);
		else open.add(id);
	}
</script>

{#snippet chips(ts: Talkgroup[], label: string)}
	<ul class="chips" aria-label={label}>
		{#each ts as t (keyOf(t))}
			<li><Chip talkgroup={t} on={on.has(keyOf(t))} priority={prio.has(keyOf(t))} ontoggle={() => toggle(t)} onpriority={() => star(t)} /></li>
		{/each}
	</ul>
{/snippet}

<p class="hint">Tap a talkgroup to switch it off or back on. Hold it, or tap its star, to make it priority.</p>

<section class="part" aria-labelledby="{uid}-prio">
	<h3 id="{uid}-prio">Priority <span class="n">({num(priorityShown.filter((t) => prio.has(keyOf(t))).length)})</span></h3>
	{#if priorityShown.length}
		{@render chips(priorityShown, 'Priority talkgroups')}
	{:else}
		<p class="hint">None yet. Hold a talkgroup below, or tap its star, to make it priority.</p>
	{/if}
</section>

<section class="part" aria-labelledby="{uid}-all">
	<h3 id="{uid}-all">By agency</h3>
	{#if groups.length}
		<ul class="groups" aria-label="Talkgroups on by agency">
			{#each groups as g (g.id)}
				{@const expanded = open.has(g.id)}
				<li class="group">
					<button type="button" class="ghost head" aria-expanded={expanded ? 'true' : 'false'} aria-controls="{uid}-{g.id}" onclick={() => flip(g.id)}>
						<span class="caret" aria-hidden="true">{expanded ? '▾' : '▸'}</span>
						<span class="gname">{g.name}</span>
						<span class="n">— {num(onIn(g.talkgroups))} on</span>
					</button>
					<div id="{uid}-{g.id}" hidden={!expanded}>
						{#if expanded}{@render chips(g.talkgroups, g.name)}{/if}
					</div>
				</li>
			{/each}
		</ul>
	{:else}
		<p class="hint">Nothing on. Switch on an agency or a category above, or search for a talkgroup.</p>
	{/if}
</section>

<style>
	.part {
		margin-top: var(--space-lg);
	}
	.part h3 {
		margin: 0 0 var(--space-xs);
		font-size: var(--text-md);
	}
	.n {
		color: var(--muted);
		font-weight: var(--weight-normal);
		font-variant-numeric: tabular-nums;
	}
	.hint {
		margin: 0;
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
	.groups {
		max-width: var(--measure);
		margin: 0;
		padding: 0;
		list-style: none;
		border-top: var(--border-width) solid var(--border);
	}
	.group {
		border-bottom: var(--border-width) solid var(--border);
	}
	.group > div {
		padding: 0 0 var(--space-sm);
	}
	.head {
		display: flex;
		justify-content: flex-start;
		gap: var(--space-xs);
		width: 100%;
		text-align: left;
	}
	.gname {
		font-weight: var(--weight-strong);
	}
	.caret {
		color: var(--muted);
	}
</style>
