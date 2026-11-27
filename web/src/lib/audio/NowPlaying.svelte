<script lang="ts">
	// Now playing, at the top of the page (SPEC.md R45, R113; ADR 0051). The
	// owner asked for the call's details "like an LCD on a scanner", in the
	// app's own look: every field always there, in the same place, "—" when
	// empty, cut with an ellipsis rather than wrapped, in tabular figures,
	// so nothing moves between calls or states. The controls stay in the
	// dock (Dock.svelte).
	//
	// - full (the Listen page, idle too, so starting to listen moves
	//   nothing), four rows of fixed height:
	//     status · system · agency · the call's start time
	//     the LED mark and the talkgroup's name, large
	//     TG number · unit · elapsed (the duration, once it's over)
	//     queued · priority · hold
	//   then, while listening, the last RECENT_SHOWN calls heard (3 on a
	//   phone): a tap plays one again, as Previous would (ADR 0048).
	// - slim (every other page, while listening): one line of name ·
	//   status · queued (not on a phone) · start time.
	// Both stay in view under the frame's top bar (sticky, at the frame's
	// own --topbar-height), and the page's scroll padding keeps a focused
	// control from going under them.
	//
	// Between calls the last call stays on the display, as a scanner's does,
	// with the status Waiting. The start time is when the call was
	// transmitted, in the deployment's time zone: never the time now, so
	// there's no wall clock (the owner's call).
	//
	// The unit: live calls carry {id, name}, where the server has already
	// put the unit's alias in name, or its ID when it has none (R27, R36). It
	// is null until a transmission names one, and changes per transmission.
	// Shown as name, so never the alias and the ID both.
	//
	// Screen readers: one polite live region says the talkgroup when the
	// call changes, and nothing else; the ticking fields aren't in it.
	//
	// data-state on the display (idle, connecting, waiting, live, behind,
	// delayed, paused) is the status's, for styles and the browser tests.
	import { liveBadge, LIVE_WITHIN } from 'archiveux/live.js';
	import { RECENT_SHOWN, type Player, type CallInfo } from './player.svelte.ts';
	import { listeningState } from '#lib/listening/state.svelte.ts';
	import { keyOf } from '#lib/listening/set.ts';
	import { needZone, zone } from '#lib/zone.svelte.ts';

	let { player, variant = 'full' }: { player: Player; variant?: 'full' | 'slim' } = $props();

	const listening = listeningState();
	const DASH = '—';

	const playing = $derived(player.playing);
	// Nothing playing: the last call stays on the display.
	const c: CallInfo | undefined = $derived(player.phase === 'stopped' ? undefined : (playing ?? player.last));

	$effect(() => {
		if (c) needZone();
	});
	const timeFmt = $derived.by(() => {
		const opts = { hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' } as const;
		try {
			return new Intl.DateTimeFormat('en-AU', { ...opts, timeZone: zone.name });
		} catch {
			return new Intl.DateTimeFormat('en-AU', { ...opts, timeZone: 'UTC' });
		}
	});
	const at = (iso: string | undefined) => {
		if (!iso) return DASH;
		const d = new Date(iso);
		return Number.isNaN(d.getTime()) ? DASH : timeFmt.format(d);
	};

	const clock = (s: number) => `${Math.floor(s / 60)}:${String(Math.floor(s) % 60).padStart(2, '0')}`;

	const status = $derived.by((): { state: string; text: string } => {
		if (player.phase === 'stopped') return { state: 'idle', text: 'Idle' };
		if (player.phase === 'starting' || !player.connected) return { state: 'connecting', text: 'Connecting' };
		if (player.paused) return { state: 'paused', text: 'Paused' };
		if (!playing) return { state: 'waiting', text: 'Waiting' };
		if (playing.delayMinutes) return liveBadge({ delay: playing.delayMinutes });
		const b = liveBadge({ behind: player.position.behind, liveWithin: LIVE_WITHIN });
		// Short, so it fits its fixed column: "Behind 9 s", "Behind 2:05".
		const s = Math.floor(player.position.behind);
		return b.state === 'behind' ? { state: b.state, text: `Behind ${s < 60 ? `${s} s` : clock(s)}` } : b;
	});

	const where = $derived(c ? [c.system?.name, c.talkgroup.groups?.[0]].filter(Boolean).join(' · ') || DASH : DASH);
	const unit = $derived(c?.unit ? c.unit.name || String(c.unit.id) : DASH);
	const time = $derived(!c ? DASH : c === playing ? clock(player.position.elapsed) : c.durationMs !== undefined ? clock(Math.round(c.durationMs / 1000)) : DASH);
	const priority = $derived(!!c && listening.saved.some((e) => e.priority && keyOf(e) === keyOf({ system: c.system?.id, id: c.talkgroup.id })));
	const queued = $derived(
		player.phase === 'stopped' ? `${DASH} queued` : `${player.queued} queued${player.heldBack ? `, ${player.heldBack} after hold` : ''}`
	);

	// The live region's words: only when the call playing changes.
	let said = $state('');
	let saidFor = '';
	$effect(() => {
		const p = player.playing;
		if (!p || p.callId === saidFor) return;
		saidFor = p.callId;
		said = `Now playing ${p.talkgroup.name}${p.talkgroup.groups?.[0] ? `, ${p.talkgroup.groups[0]}` : ''}`;
	});

	const others = $derived(player.recent.filter((h) => h.info.callId !== playing?.callId).slice(0, RECENT_SHOWN));

	// The page's scroll padding: the top bar and this, so the browser
	// scrolls a focused control (or a #link) out from under both.
	let place: HTMLElement | undefined = $state();
	$effect(() => {
		const el = place;
		if (!el) return;
		const root = document.documentElement;
		const fit = () => {
			const room = `${(parseFloat(getComputedStyle(el).top) || 0) + el.offsetHeight}px`;
			root.style.scrollPaddingTop = room;
		};
		const watch = new ResizeObserver(fit);
		watch.observe(el);
		addEventListener('resize', fit);
		fit();
		return () => {
			watch.disconnect();
			removeEventListener('resize', fit);
			root.style.scrollPaddingTop = '';
		};
	});
</script>

{#snippet field(text: string, cls: string, label?: string)}
	<span class={['f', cls]} title={text === DASH ? undefined : text}
		>{#if label}<span class="visually-hidden">{`${label} `}</span>{/if}{text}</span
	>
{/snippet}

<!-- Sized by its widest value, so "—" and a time take the same room. -->
{#snippet sized(text: string, cls: string, widest: string, label?: string)}
	<span class={['f', 'sized', cls]}
		><span class="sizer" aria-hidden="true">{widest}</span><span class="value"
			>{#if label}<span class="visually-hidden">{`${label} `}</span>{/if}{text}</span
		></span
	>
{/snippet}

<p class="visually-hidden" aria-live="polite">{said}</p>

<div class="place" class:slim={variant === 'slim'} bind:this={place}>
	{#if variant === 'slim'}
		<section class="now-playing slim" aria-label="Now playing" data-state={status.state} data-variant="slim">
			{@render field(c?.talkgroup.name ?? DASH, 'name')}
			<span class="live-badge status" data-state={status.state} title={status.text}>{status.text}</span>
			{@render field(queued, 'queued')}
			{@render sized(at(c?.startedAt), 'started', '00:00:00', 'Started')}
		</section>
	{:else}
		<section class="now-playing full" aria-label="Now playing" data-state={status.state} data-variant="full">
			<div class="row r1">
				<span class="live-badge status" data-state={status.state} title={status.text}>{status.text}</span>
				{@render field(where, 'where')}
				{@render sized(at(c?.startedAt), 'started', '00:00:00', 'Started')}
			</div>
			<div class="row r2">
				{#if c?.talkgroup.led_colour}<span class="led" data-led={c.talkgroup.led_colour} aria-hidden="true"></span>{/if}
				{@render field(c?.talkgroup.name ?? DASH, 'name')}
			</div>
			<div class="row r3">
				{@render field(c ? `TG ${c.talkgroup.id}` : `TG ${DASH}`, 'tg')}
				{@render field(unit, 'unit', 'Unit')}
				{@render sized(time, 'elapsed', '00:00', c && c === playing ? 'Duration so far' : 'Duration')}
			</div>
			<div class="row r4">
				{@render field(queued, 'queued')}
				<span class="f priority" class:on={priority}
					><span aria-hidden="true">{priority ? '★' : '☆'}</span>{` ${c ? (priority ? 'Priority' : 'Not priority') : DASH}`}</span
				>
				<span class="f hold" class:on={!!player.hold}>{player.phase === 'stopped' ? `Hold ${DASH}` : `Hold ${player.hold ? 'on' : 'off'}`}</span>
			</div>
		</section>
	{/if}
</div>

{#if variant === 'full' && player.phase !== 'stopped'}
	<!-- Always RECENT_SHOWN places while listening, "—" until filled, so a call coming never moves the page. -->
	<ol class="recent" aria-label="Recent calls heard">
		{#each { length: RECENT_SHOWN } as _, i (i)}
			{@const h = others[i]}
			<li>
				{#if h}
					<button type="button" class="ghost" onclick={() => player.playHeard(h.seq)}
						><span class="visually-hidden">{'Play again: '}</span><span class="rn">{h.info.talkgroup.name}</span><span class="rt"
							><span class="visually-hidden">{', started '}</span>{at(h.info.startedAt)}</span
						></button
					>
				{:else}
					<span class="empty" aria-hidden="true"><span class="rn">{DASH}</span><span class="rt">{DASH}</span></span>
				{/if}
			</li>
		{/each}
	</ol>
{/if}

<style>
	/* In view under the frame's top bar while the page scrolls, across the
	   page's width (into its padding) so nothing scrolls past beside it. */
	.place {
		position: sticky;
		top: var(--topbar-height, 0);
		z-index: var(--layer-header);
		margin: 0 calc(-1 * var(--page-pad)) var(--space-lg);
		padding: 0 var(--page-pad) var(--space-xs);
		background: var(--bg);
	}
	.place.slim {
		margin-bottom: var(--space-md);
	}
	@media (max-width: 47.99rem) {
		.place {
			margin-inline: calc(-1 * var(--page-pad-phone));
			padding-inline: var(--page-pad-phone);
		}
	}
	.now-playing {
		max-width: var(--measure);
		background: var(--surface);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
		font-variant-numeric: tabular-nums;
	}
	.full {
		display: grid;
		gap: var(--space-2xs);
		padding: var(--space-sm) var(--space-md);
	}
	.row {
		display: grid;
		align-items: center;
		/* Each row is one line high (its height, below), whatever is in it: nothing moves between calls. */
		overflow: hidden;
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
	/* The widest value and the value in one cell: the column is always the widest's width. */
	.sized {
		display: inline-grid;
		justify-items: end;
	}
	.sized > * {
		grid-area: 1 / 1;
	}
	.sizer {
		visibility: hidden;
	}
	/* Fixed columns: the status and time never push the middle about. The
	   first row is as high as the Delayed badge, padding and all. */
	.r1 {
		grid-template-columns: var(--label-min) minmax(0, 1fr) auto;
		height: calc(var(--leading-tight) * var(--text-sm) + 2 * var(--badge-pad-y));
		font-size: var(--text-sm);
	}
	.status {
		min-width: 0;
		max-width: 100%;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.where {
		color: var(--muted);
	}
	.started {
		font-family: var(--font-meta);
	}
	.r2 {
		display: flex;
		align-items: center;
		gap: var(--space-xs);
		height: calc(var(--leading-tight) * var(--text-2xl));
	}
	.r2 .led {
		flex: none;
	}
	.name {
		font-size: var(--text-2xl);
		font-weight: var(--weight-heavy);
	}
	.r3 {
		grid-template-columns: var(--label-min) minmax(0, 1fr) auto;
		height: calc(var(--leading-tight) * var(--text-base));
		font-family: var(--font-meta);
	}
	.r4 {
		grid-template-columns: repeat(3, minmax(0, 1fr));
		height: calc(var(--leading-tight) * var(--text-sm));
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.r4 .priority {
		text-align: center;
	}
	.r4 .hold {
		text-align: right;
	}
	.r4 .on {
		color: var(--fg);
		font-weight: var(--weight-strong);
	}
	/* Not playing: a hollow dot, muted, as ArchiveUx draws Behind. */
	.live-badge[data-state='idle'],
	.live-badge[data-state='connecting'],
	.live-badge[data-state='waiting'],
	.live-badge[data-state='paused'] {
		color: var(--muted);
		font-weight: var(--weight-normal);
	}
	.live-badge[data-state='idle']::before,
	.live-badge[data-state='connecting']::before,
	.live-badge[data-state='waiting']::before {
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
		grid-template-columns: minmax(0, 1fr) auto auto auto;
		align-items: center;
		gap: var(--space-md);
		max-width: none;
		height: calc(var(--leading-tight) * var(--text-base) + 2 * var(--badge-pad-y) + 2 * var(--space-2xs));
		padding: 0 var(--space-md);
		overflow: hidden;
		line-height: var(--leading-tight);
	}
	.slim .name {
		font-size: var(--text-base);
		font-weight: var(--weight-strong);
	}
	.slim .queued {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.slim .started {
		font-size: var(--text-sm);
	}
	/* Phones: the name needs the room more than the queue count does. */
	@media (max-width: 47.99rem) {
		.slim {
			grid-template-columns: minmax(0, 1fr) auto auto;
		}
		.slim .queued {
			display: none;
		}
	}
	.recent {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: var(--space-2xs) var(--space-md);
		max-width: var(--measure);
		margin: calc(-1 * var(--space-md)) 0 var(--space-lg);
		padding: 0;
		list-style: none;
		font-size: var(--text-sm);
	}
	.recent li {
		display: flex;
		min-width: 0;
	}
	/* Phones: one column, so the names have room; three calls. */
	@media (max-width: 47.99rem) {
		.recent {
			grid-template-columns: minmax(0, 1fr);
			gap: 0;
		}
		.recent li:nth-child(n + 4) {
			display: none;
		}
	}
	.recent button,
	.recent .empty {
		display: flex;
		flex: 1 1 auto;
		justify-content: space-between;
		gap: var(--space-sm);
		min-width: 0;
		min-height: var(--target);
		padding: 0 var(--space-sm);
		font-weight: var(--weight-normal);
		color: var(--fg);
	}
	.recent .empty {
		align-items: center;
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
