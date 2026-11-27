<script lang="ts">
	// Now playing, at the top of the page (POC). The owner: "treat it like an
	// LCD on a scanner", in the app's own look. Every field is always there,
	// in the same place, "—" when empty, cut with an ellipsis rather than
	// wrapped, so nothing moves between calls.
	//
	// - full (the Listen page), four rows:
	//     status · system · agency · the call's start time
	//     the talkgroup's name, large
	//     TG number · unit · elapsed (or the duration, once it's over)
	//     queued · priority · hold
	//   and, under it, the last few calls (Recent, easy to take out: the
	//   `recent` prop).
	// - slim (every other page, while listening): name · status · start time.
	//
	// The start time is when the call was transmitted, in the deployment's
	// time zone, never the time now: no wall clock (the owner's call).
	//
	// The unit: live calls carry {id, name}, where the server has already
	// put the unit's alias in name, or its ID when it has none (R27, R36);
	// it's null until a transmission names one, and changes per
	// transmission. Shown as name, so never alias and ID both.
	//
	// Screen readers: one polite live region says the talkgroup when the
	// call changes, and nothing else; the ticking fields aren't in it.
	import { onMount } from 'svelte';
	import { liveBadge, LIVE_WITHIN } from 'archiveux/live.js';
	import type { Player, CallInfo } from './player.svelte.ts';
	import { listeningState } from '#lib/listening/state.svelte.ts';
	import { keyOf } from '#lib/listening/set.ts';

	let { player, variant = 'full', recent = true }: { player: Player; variant?: 'full' | 'slim'; recent?: boolean } = $props();

	const listening = listeningState();
	const DASH = '—';

	let zone = $state('UTC');
	onMount(async () => {
		try {
			const r = await fetch('/api/v1/catalogue', { headers: { Accept: 'application/json' } });
			if (r.ok) zone = ((await r.json()) as { time_zone?: string }).time_zone || 'UTC';
		} catch {
			// UTC, as the admin pages fall back to.
		}
	});
	const timeFmt = $derived(new Intl.DateTimeFormat('en-AU', { timeZone: zone, hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }));
	const at = (iso: string | undefined) => {
		if (!iso) return DASH;
		const d = new Date(iso);
		return Number.isNaN(d.getTime()) ? DASH : timeFmt.format(d);
	};

	const playing = $derived(player.playing);
	// Nothing playing: the last call stays on the display, as a scanner's does.
	const c: CallInfo | undefined = $derived(player.phase === 'stopped' ? undefined : (playing ?? player.last));

	const status = $derived.by((): { state: string; text: string } => {
		if (player.phase === 'stopped') return { state: 'idle', text: 'Idle' };
		if (player.phase === 'starting' || !player.connected) return { state: 'idle', text: 'Connecting' };
		if (player.paused) return { state: 'paused', text: 'Paused' };
		if (!playing) return { state: 'idle', text: player.hold ? 'Waiting (hold)' : 'Waiting' };
		const b = playing.delayMinutes ? liveBadge({ delay: playing.delayMinutes }) : liveBadge({ behind: player.position.behind, liveWithin: LIVE_WITHIN });
		return b;
	});

	const clock = (s: number) => `${Math.floor(s / 60)}:${String(Math.floor(s) % 60).padStart(2, '0')}`;
	const where = $derived(c ? [c.system?.name, c.talkgroup.groups?.[0]].filter(Boolean).join(' · ') || DASH : DASH);
	const unit = $derived(c?.unit ? c.unit.name || String(c.unit.id) : DASH);
	const time = $derived(
		!c ? DASH : c === playing ? clock(player.position.elapsed) : c.durationMs !== undefined ? clock(Math.round(c.durationMs / 1000)) : DASH
	);
	const priority = $derived(!!c && listening.saved.some((e) => e.priority && keyOf(e) === keyOf({ system: c.system?.id, id: c.talkgroup.id })));
	const queued = $derived(player.phase === 'stopped' ? DASH : String(player.queued));

	// The live region's words: only when the call playing changes.
	let said = $state('');
	let saidFor = '';
	$effect(() => {
		const p = player.playing;
		if (!p || p.callId === saidFor) return;
		saidFor = p.callId;
		said = `Now playing ${p.talkgroup.name}${p.talkgroup.groups?.[0] ? `, ${p.talkgroup.groups[0]}` : ''}`;
	});

	const others = $derived(player.recent.filter((h) => h.info.callId !== playing?.callId).slice(0, 4));
</script>

{#snippet field(text: string, cls: string, label?: string)}
	<span class={['f', cls]} title={text === DASH ? undefined : text}
		>{#if label}<span class="visually-hidden">{label} </span>{/if}{text}</span
	>
{/snippet}

<p class="visually-hidden" aria-live="polite">{said}</p>

{#if variant === 'slim'}
	<div class="np slim" role="group" aria-label="Now playing">
		{@render field(c?.talkgroup.name ?? DASH, 'name')}
		<span class="live-badge status" data-state={status.state}>{status.text}</span>
		{@render field(at(c?.startedAt), 'time', 'Started')}
	</div>
{:else}
	<div class="np full" role="group" aria-label="Now playing" data-state={status.state}>
		<div class="row r1">
			<span class="live-badge status" data-state={status.state}>{status.text}</span>
			{@render field(where, 'where')}
			{@render field(at(c?.startedAt), 'time', 'Started')}
		</div>
		<div class="row r2">{@render field(c?.talkgroup.name ?? DASH, 'name')}</div>
		<div class="row r3">
			{@render field(c ? `TG ${c.talkgroup.id}` : 'TG —', 'tg')}
			{@render field(unit, 'unit', 'Unit')}
			{@render field(time, 'elapsed', c === playing ? 'Duration so far' : 'Duration')}
		</div>
		<div class="row r4">
			<span class="f">{queued} queued</span>
			<span class="f" class:on={priority}><span aria-hidden="true">{priority ? '★' : '☆'}</span> {priority ? 'Priority' : 'Not priority'}</span>
			<span class="f" class:on={!!player.hold}>Hold {player.hold ? 'on' : 'off'}</span>
		</div>
	</div>
	{#if recent && player.phase !== 'stopped'}
		<!-- POC, optional: the last few calls. Remove this block and the `recent` prop to take it out. -->
		<ol class="recent" aria-label="Recent calls heard">
			{#each { length: 4 } as _, i (i)}
				{@const h = others[i]}
				<li>
					{#if h}
						<button type="button" class="ghost" onclick={() => player.playHeard(h.seq)} title="Play {h.info.talkgroup.name} again">
							<span class="rn">{h.info.talkgroup.name}</span><span class="rt"><span class="visually-hidden">started </span>{at(h.info.startedAt)}</span>
						</button>
					{:else}
						<span class="empty"><span class="rn">{DASH}</span><span class="rt">{DASH}</span></span>
					{/if}
				</li>
			{/each}
		</ol>
	{/if}
{/if}

<style>
	.np {
		max-width: var(--measure);
		background: var(--surface);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
		font-variant-numeric: tabular-nums;
	}
	.full {
		display: grid;
		gap: var(--space-2xs);
		margin: 0 0 var(--space-lg);
		padding: var(--space-sm) var(--space-md);
	}
	.row {
		display: grid;
		align-items: baseline;
		gap: var(--space-md);
		min-width: 0;
		line-height: var(--leading-tight);
	}
	.f {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	/* Fixed columns: the status and time never push the middle about. */
	.r1 {
		grid-template-columns: var(--label-min) minmax(0, 1fr) auto;
	}
	.r1 .status {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.where {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.time {
		font-family: var(--font-meta);
		font-size: var(--text-sm);
		text-align: right;
	}
	.r2 {
		grid-template-columns: minmax(0, 1fr);
	}
	.name {
		font-size: var(--text-xl);
		font-weight: var(--weight-heavy);
	}
	.r3 {
		grid-template-columns: var(--label-min) minmax(0, 1fr) auto;
		font-family: var(--font-meta);
	}
	.elapsed {
		text-align: right;
	}
	.r4 {
		grid-template-columns: repeat(3, minmax(0, 1fr));
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.r4 .f:nth-child(2) {
		text-align: center;
	}
	.r4 .f:nth-child(3) {
		text-align: right;
	}
	.r4 .on {
		color: var(--fg);
		font-weight: var(--weight-strong);
	}
	.live-badge[data-state='idle'],
	.live-badge[data-state='paused'] {
		color: var(--muted);
		font-weight: var(--weight-normal);
	}
	.live-badge[data-state='idle']::before {
		background: transparent;
		border: var(--border-width-strong) solid var(--muted);
	}
	/* Paused: two bars, not a dot, so it isn't told by colour alone. */
	.live-badge[data-state='paused']::before {
		border-radius: 0;
		background: transparent;
		border-left: var(--border-width-strong) solid var(--muted);
		border-right: var(--border-width-strong) solid var(--muted);
	}
	.slim {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto auto;
		align-items: center;
		gap: var(--space-md);
		max-width: none;
		margin: 0 0 var(--space-md);
		padding: var(--space-2xs) var(--space-md);
		line-height: var(--leading-tight);
	}
	.slim .name {
		font-size: var(--text-base);
		font-weight: var(--weight-strong);
	}
	.recent {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: var(--space-2xs) var(--space-md);
		max-width: var(--measure);
		margin: calc(-1 * var(--space-sm)) 0 var(--space-lg);
		padding: 0;
		list-style: none;
		font-size: var(--text-sm);
	}
	.recent li {
		display: flex;
		min-width: 0;
	}
	.recent button,
	.recent .empty {
		display: flex;
		flex: 1 1 auto;
		justify-content: space-between;
		gap: var(--space-sm);
		min-width: 0;
		padding: 0 var(--space-sm);
		font-weight: var(--weight-normal);
		color: var(--fg);
	}
	.recent .empty {
		align-items: center;
		min-height: var(--target);
		color: var(--muted);
	}
	.rn {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.rt {
		flex: none;
		color: var(--muted);
		font-family: var(--font-meta);
		font-variant-numeric: tabular-nums;
	}
</style>
