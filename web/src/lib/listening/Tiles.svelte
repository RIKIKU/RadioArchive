<script lang="ts">
	// The listening set as tiles (SPEC.md R42; ADR 0048). A top box with how
	// many talkgroups are on, All on and All off, and what's on; then, per
	// system, a tile per group (Agencies) and per tag (Categories). One tap
	// on a tile switches all of its talkgroups on, as they are now, or off
	// when they're all on already. Each tile says whether it's all on,
	// partly on or off by its mark (filled, half, empty), its count in words
	// and aria-pressed (true, mixed, false), never by colour alone. Single
	// talkgroups are reached by the search (Picker.svelte), not drawn here:
	// a deployment may have over a thousand.
	import { keyOf, type Index, type Talkgroup } from './set.ts';
	import type { ListeningState } from './state.svelte.ts';

	let { ix, listening }: { ix: Index; listening: ListeningState } = $props();

	interface Tile {
		id: string;
		name: string;
		talkgroups: Talkgroup[];
	}
	interface Section {
		id: string;
		system?: string;
		agencies: Tile[];
		categories: Tile[];
	}
	type State = 'on' | 'mixed' | 'off';

	const all = $derived(ix.catalogue.talkgroups);
	const on = $derived(new Set(listening.saved.map(keyOf)));

	const sections: Section[] = $derived.by(() => {
		const systems = ix.catalogue.systems?.length ? ix.catalogue.systems : [undefined];
		return systems.map((s) => {
			const sid = s?.id ?? 0;
			const mine = all.filter((t) => s === undefined || t.system === s.id);
			const agencies: Tile[] = ix.catalogue.groups
				.map((g, i) => ({ id: `${sid}-g${i}`, name: g, talkgroups: mine.filter((t) => t.groups.includes(g)) }))
				.filter((g) => g.talkgroups.length);
			const loose = mine.filter((t) => !t.groups.length);
			if (loose.length) agencies.push({ id: `${sid}-gnone`, name: 'Not in a group', talkgroups: loose });
			const categories: Tile[] = ix.catalogue.tags
				.map((tag, i) => ({ id: `${sid}-t${i}`, name: tag, talkgroups: mine.filter((t) => t.tag === tag) }))
				.filter((g) => g.talkgroups.length);
			return { id: String(sid), system: s ? s.name || s.label : undefined, agencies, categories };
		});
	});

	function count(ts: Talkgroup[]): number {
		let n = 0;
		for (const t of ts) if (on.has(keyOf(t))) n++;
		return n;
	}
	const stateOf = (n: number, of: number): State => (n === 0 ? 'off' : n === of ? 'on' : 'mixed');
	const pressed = (s: State) => (s === 'on' ? 'true' : s === 'mixed' ? 'mixed' : 'false');

	function press(tile: Tile, s: State) {
		if (s === 'on') listening.remove(...tile.talkgroups);
		else listening.add(...tile.talkgroups);
	}

	const total = $derived(all.length);
	const onCount = $derived(count(all));
	const num = (n: number) => n.toLocaleString('en-AU');

	// What's on, in a line: the agencies with anything on, fully or partly.
	const whatsOn = $derived(
		sections.flatMap((s) =>
			s.agencies
				.map((t) => ({ tile: t, n: count(t.talkgroups) }))
				.filter((x) => x.n > 0)
				.map((x) => ({ ...x, system: s.system }))
		)
	);
</script>

{#snippet tiles(list: Tile[], label: string)}
	<ul class="tiles" aria-label={label}>
		{#each list as t (t.id)}
			{@const n = count(t.talkgroups)}
			{@const s = stateOf(n, t.talkgroups.length)}
			<li>
				<button type="button" class="tile secondary" data-state={s} aria-pressed={pressed(s)} onclick={() => press(t, s)}>
					<span class="mark" aria-hidden="true"></span>
					<span class="name">{t.name}</span>
					<span class="count">{s === 'on' ? `All ${num(n)} on` : s === 'off' ? `${num(t.talkgroups.length)} off` : `${num(n)} of ${num(t.talkgroups.length)} on`}</span>
				</button>
			</li>
		{/each}
	</ul>
{/snippet}

<div class="box">
	<div class="box-head">
		<p class="summary" role="status"><strong>{num(onCount)}</strong> of {num(total)} talkgroups on</p>
		<div class="all">
			<button type="button" disabled={onCount === total} onclick={() => listening.add(...all)}>All on</button>
			<button type="button" class="secondary" disabled={onCount === 0} onclick={() => listening.remove(...all)}>All off</button>
		</div>
	</div>
	{#if whatsOn.length}
		<p class="whats-on">
			<span class="visually-hidden">On: </span>
			{#each whatsOn as w (w.tile.id)}
				<span class="chip" data-state={stateOf(w.n, w.tile.talkgroups.length)}
					><span class="mark" aria-hidden="true"></span>{w.tile.name}{#if w.system}<span class="sys"> ({w.system})</span>{/if}<span class="of"
						>{w.n === w.tile.talkgroups.length ? 'all' : `${num(w.n)}/${num(w.tile.talkgroups.length)}`}</span
					></span
				>
			{/each}
		</p>
	{:else}
		<p class="whats-on empty">Your listening set is empty. Tap a tile to switch on all of its talkgroups, or find one below.</p>
	{/if}
</div>

{#each sections as s (s.id)}
	{#if s.system}<h3 class="system">{s.system}</h3>{/if}
	<!-- Under the system's heading when there are several, else under the page's. -->
	{#if s.agencies.length}
		<svelte:element this={s.system ? 'h4' : 'h3'} class="row-label">Agencies</svelte:element>
		{@render tiles(s.agencies, s.system ? `Agencies in ${s.system}` : 'Agencies')}
	{/if}
	{#if s.categories.length}
		<svelte:element this={s.system ? 'h4' : 'h3'} class="row-label">Categories</svelte:element>
		{@render tiles(s.categories, s.system ? `Categories in ${s.system}` : 'Categories')}
	{/if}
{/each}

<style>
	.box {
		display: flex;
		flex-direction: column;
		gap: var(--space-sm);
		padding: var(--space-md);
		background: var(--surface);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
	}
	.box p {
		margin: 0;
	}
	.box-head {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-sm) var(--space-lg);
	}
	.summary {
		font-variant-numeric: tabular-nums;
	}
	.all {
		display: flex;
		gap: var(--space-sm);
	}
	.whats-on {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2xs) var(--space-xs);
		font-size: var(--text-sm);
	}
	.whats-on.empty {
		color: var(--muted);
	}
	.chip {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2xs);
		padding: var(--badge-pad-y) var(--badge-pad-x);
		border-radius: var(--radius-badge);
		background: var(--accent-soft);
		color: var(--accent-soft-fg);
	}
	.chip .of {
		margin-left: var(--space-2xs);
		font-family: var(--font-meta);
		font-variant-numeric: tabular-nums;
	}
	.sys {
		font-weight: var(--weight-normal);
	}
	.system {
		margin: var(--space-xl) 0 0;
		font-size: var(--text-lg);
	}
	.row-label {
		margin: var(--space-lg) 0 var(--space-xs);
		color: var(--muted);
		font-size: var(--text-sm);
		font-weight: var(--weight-strong);
	}
	.tiles {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(calc(2 * var(--target) + var(--space-lg)), 1fr));
		gap: var(--space-xs);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.tiles li {
		display: flex;
	}
	.tile {
		display: flex;
		flex: 1 1 auto;
		flex-direction: column;
		align-items: flex-start;
		justify-content: flex-start;
		gap: var(--space-2xs);
		min-width: 0;
		padding: var(--space-xs) var(--space-sm);
		text-align: left;
		font-weight: var(--weight-normal);
	}
	.tile .name {
		font-weight: var(--weight-strong);
		font-size: var(--text-sm);
		overflow-wrap: anywhere;
	}
	.tile .count {
		margin-top: auto;
		color: var(--muted);
		font-family: var(--font-meta);
		font-size: var(--text-xxs);
		font-variant-numeric: tabular-nums;
	}
	.tile[data-state='on'] {
		background: var(--accent-soft);
		border-color: var(--accent);
		color: var(--accent-soft-fg);
	}
	.tile[data-state='on'] .count {
		color: var(--accent-soft-fg);
	}
	.tile[data-state='mixed'] {
		border-color: var(--accent);
	}
	/* The mark: empty (off), half (partly on), filled (all on). */
	.mark {
		flex: none;
		display: inline-block;
		width: var(--badge-dot-size);
		height: var(--badge-dot-size);
		border: var(--border-width-strong) solid var(--accent);
		border-radius: var(--radius-round);
		background: transparent;
	}
	.tile .mark {
		width: var(--control-check);
		height: var(--control-check);
	}
	.chip .mark {
		border-color: var(--accent-soft-fg);
	}
	[data-state='on'] > .mark {
		background: var(--accent);
	}
	.chip[data-state='on'] > .mark {
		background: var(--accent-soft-fg);
	}
	[data-state='mixed'] > .mark {
		background: linear-gradient(90deg, var(--accent) 50%, transparent 50%);
	}
	.chip[data-state='mixed'] > .mark {
		background: linear-gradient(90deg, var(--accent-soft-fg) 50%, transparent 50%);
	}
</style>
