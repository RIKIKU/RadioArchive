<script lang="ts">
	// The dock's player (SPEC.md R44, R45, R81; ADR 0012, 0048), in
	// ArchiveUx's frame dock: Start listening, then now playing and the
	// controls.
	//
	// POC: live listening's now playing has moved to the top of the page
	// (NowPlaying.svelte); the dock keeps its controls, and the line below
	// them only while Catch up, Previous's count or an action's result
	// shows. What follows describes main's dock.
	//
	// Now playing: the LED colour mark beside the talkgroup's name (never the
	// only cue: the name is there), its group, the transmitting unit's alias
	// or ID (updated per transmission), the system only when there are
	// several, Live or Behind by N s or Delayed N min (ArchiveUx's badge),
	// the call's duration so far, and how many calls are queued. Hold shows
	// in words, beside the queue and on its pressed button. Only when behind
	// (calls queued, or the call playing further back than Live allows),
	// "Catch up" in words by the queue count brings it back to live.
	//
	// Controls (R44; ADR 0048), in this order: Pause or Resume, Previous,
	// Skip, Hold, Stop, then Share (R45, ADR 0017) and Add to basket (ADR
	// 0018), which act on the playing call. Previous is the skip icon facing
	// the other way; while its presses are being counted the queue line says
	// "Back 3 calls…". Each control has its name in words beside its icon
	// when there's room for them all in one row, and a short name under its
	// icon when there isn't. On a phone, Share and Add to basket go in an
	// Actions menu, which opens upwards, and what they did shows by the
	// queue count.
	//
	// A call replayed from recent calls (R46; ADR 0016) shows first, with
	// its own Stop, Share and Add to basket, whether live listening is
	// paused or never started; on a phone they're laid out as live
	// listening's are. Several played in turn (Play all, play from here: ADR
	// 0023) show which call of them is playing, and when it started.
	//
	// On a phone (ADR 0023, "On a phone") the dock stacks: the replay first,
	// its name beside its controls and the rest under both, then
	// live listening, its status in a line above its controls. On a desktop
	// the two sit side by side, as before (the wrapper isn't drawn there).
	import { Icon, MoreActions } from 'archiveux/components';
	import { LIVE_WITHIN } from 'archiveux/live.js';
	import type { Player, CallInfo } from './player.svelte.ts';
	import { dockIcons } from './icons.ts';
	import ShareButton from '#lib/share/ShareButton.svelte';
	import AddToBasket from '#lib/basket/AddToBasket.svelte';
	import { callLink, copyText } from '#lib/share/link.ts';
	import { basketState } from '#lib/basket/state.svelte.ts';

	let { player }: { player: Player } = $props();

	const basket = basketState();
	// Behind the radio: calls waiting, or the call playing further back than
	// Live allows. A delayed talkgroup's call is behind on purpose.
	const behind = $derived(player.queued > 0 || (!!player.playing && !player.playing.delayMinutes && player.position.behind > LIVE_WITHIN));

	// What an Actions menu's action did, by the queue count or the replay's
	// details (the menu closes).
	let said: { where: 'player' | 'replay'; text: string } | undefined = $state();
	let saidTimer: ReturnType<typeof setTimeout> | undefined;
	function say(where: 'player' | 'replay', text: string) {
		clearTimeout(saidTimer);
		said = { where, text };
		saidTimer = setTimeout(() => (said = undefined), 4000);
	}
	async function shareCall(where: 'player' | 'replay', id: string) {
		say(where, (await copyText(callLink(id))) ? 'Link copied' : "Couldn't copy the link");
	}
	function basketCall(where: 'player' | 'replay', id: string) {
		if (basket.has(id)) {
			basket.remove(id);
			say(where, 'Taken out of the basket');
		} else {
			basket.add(id);
			say(where, basket.has(id) ? 'Added to the basket' : 'The basket is full');
		}
	}

	const c = $derived(player.playing);
	const r = $derived(player.replay.call);
	const clock = (s: number) => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
	const unitOf = (i: CallInfo) => (i.unit ? i.unit.name || String(i.unit.id) : 'Unit unknown');
	const queuedText = $derived(
		player.queued === 0
			? 'No calls queued'
			: `${player.queued} queued${player.heldBack ? `, ${player.heldBack} after hold` : ''}`
	);
</script>

{#snippet actions(where: 'player' | 'replay', id: string, name: string)}
	<span class="wide-only"><ShareButton callId={id} label={name} icon /><AddToBasket callId={id} label={name} icon /></span>
	<span class="phone-only dock-more">
		<MoreActions label="More actions: {name}" text="Actions">
			<button type="button" onclick={() => shareCall(where, id)}><Icon svg={dockIcons.share} />Share link</button>
			<button type="button" aria-pressed={basket.has(id)} onclick={() => basketCall(where, id)}
				><Icon svg={dockIcons.basket} />{basket.has(id) ? 'Take out of basket' : 'Add to basket'}</button
			>
		</MoreActions>
	</span>
{/snippet}

<div class="stack" class:replaying={!!r}>
{#if r}
	<div class="replay" data-replay={r.callId}>
		<div class="what" aria-live="polite">
			<p class="np-name">
				<span class="replay-mark">Replaying</span>
				{#if r.talkgroup.led_colour}<span class="led" data-led={r.talkgroup.led_colour} aria-hidden="true"></span>{/if}
				<span class="tg">{r.talkgroup.name}</span>{#if r.talkgroup.groups?.[0]}<span class="group">{r.talkgroup.groups[0]}</span>{/if}
			</p>
			<p class="meta np-unit">
				<span class="unit">{unitOf(r)}</span>{#if r.system}<span class="sep" aria-hidden="true">·</span><span class="system">{r.system.name}</span>{/if}
				<span class="sep" aria-hidden="true">·</span><span class="elapsed"><span class="visually-hidden">{'Played '}</span>{clock(player.replay.elapsed)}</span>
				{#if player.replay.list}{@const l = player.replay.list}<span class="sep" aria-hidden="true">·</span><span class="list">{l.name}: call {l.at} of {l.more ? `at least ${l.of}` : l.of}</span
					>{#if l.when}<span class="sep" aria-hidden="true">·</span><span class="when"><span class="visually-hidden">{'Started '}</span>{l.when}</span>{/if}{/if}
				{#if said?.where === 'replay'}<span class="said" role="status">{said.text}</span>{/if}
			</p>
		</div>
		<div class="controls" role="group" aria-label="Replay controls">
			<button type="button" class="secondary" onclick={() => player.replay.stop()}
				><Icon svg={dockIcons.stop} /><span class="label">Stop<span class="long">{' replay'}</span></span></button
			>
			{@render actions('replay', r.callId, r.talkgroup.name)}
		</div>
	</div>
{:else if player.replay.failure}
	<p class="error replay-failed" role="alert">{player.replay.failure}</p>
{/if}
{#if player.phase === 'stopped'}
	<div class="idle">
		<button type="button" class="start" onclick={() => player.start()}><Icon name="play" />Start listening</button>
		{#if player.failure}
			<p class="error" role="alert">{player.failure}</p>
		{:else}
			<p class="meta">Hear your listening set live.</p>
		{/if}
	</div>
{:else}
	<div class="player" data-decoder={player.path ?? ''} data-hold={player.hold ? 'on' : 'off'} data-paused={player.paused ? 'yes' : 'no'}>
		<!-- POC: what's playing is at the top of the page now (NowPlaying.svelte);
		     the dock keeps the controls, and a line only for what comes and goes. -->
		{#if behind || player.stepsBack || said?.where === 'player'}
			<p class="meta queue">
				{#if behind}<span class="queued">{queuedText}</span><span class="sep" aria-hidden="true">·</span><button type="button" class="ghost catch-up" onclick={() => player.catchUp()}>Catch up<span class="visually-hidden"> to live</span></button>{/if}
				{#if player.stepsBack}<span class="said" role="status">Back {player.stepsBack} {player.stepsBack === 1 ? 'call' : 'calls'}…</span>{/if}
				{#if said?.where === 'player'}<span class="said" role="status">{said.text}</span>{/if}
			</p>
		{/if}
		<div class="bar">
			<div class="controls" role="group" aria-label="Player controls">
				{#if player.paused}
					<button type="button" class="secondary" onclick={() => player.resume()}><Icon name="play" /><span class="label">Resume</span></button>
				{:else}
					<button type="button" class="secondary" onclick={() => player.pause()}><Icon name="pause" /><span class="label">Pause</span></button>
				{/if}
				<button type="button" class="secondary" disabled={!player.canPrevious} onclick={() => player.previous()}
					><span class="flip"><Icon name="skip" /></span><span class="label">Previous</span></button
				>
				<button type="button" class="secondary" disabled={!c} onclick={() => player.skip()}><Icon name="skip" /><span class="label">Skip</span></button>
				<button type="button" class="secondary" aria-pressed={!!player.hold} disabled={!c && !player.hold} onclick={() => player.setHold(!player.hold)}
					><Icon svg={dockIcons.hold} /><span class="label">Hold</span></button
				>
				<button type="button" class="secondary" onclick={() => player.stop()}
					><Icon svg={dockIcons.stop} /><span class="label">Stop<span class="long">{' listening'}</span></span></button
				>
				{#if c}{@render actions('player', c.callId, c.talkgroup.name)}{/if}
			</div>
		</div>
	</div>
{/if}
</div>

<style>
	/* Desktops: not drawn, so what's in it sits in the frame's dock row. */
	.stack {
		display: contents;
	}
	.replay {
		display: flex;
		flex: 1 1 100%;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-xs) var(--space-lg);
		min-width: 0;
		padding-bottom: var(--space-xs);
		border-bottom: var(--border-width) solid var(--border);
	}
	.replay p,
	.replay-failed {
		margin: 0;
	}
	.replay-failed {
		flex: 1 1 100%;
	}
	.replay-mark {
		padding: var(--badge-pad-y) var(--badge-pad-x);
		border-radius: var(--radius-badge);
		background: var(--accent-soft);
		color: var(--accent-soft-fg);
		font-size: var(--text-sm);
	}
	.idle,
	.player {
		display: flex;
		flex: 1 1 auto;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-sm) var(--space-lg);
		min-width: 0;
	}
	.idle p {
		margin: 0;
	}
	.start {
		gap: var(--space-xs);
	}
	.queue {
		margin: 0;
	}
	.what {
		flex: 1 1 auto;
		min-width: 0;
	}
	.np-name {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0 var(--space-xs);
		font-weight: var(--weight-strong);
		overflow-wrap: anywhere;
	}
	.group {
		color: var(--muted);
		font-weight: var(--weight-normal);
	}
	.group::before {
		content: '·';
		margin-right: var(--space-xs);
	}
	.np-unit {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-xs);
	}
	.elapsed {
		font-variant-numeric: tabular-nums;
	}
	.queue {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2xs) var(--space-sm);
		flex-basis: 100%;
	}
	.meta {
		color: var(--muted);
		font-family: var(--font-meta);
		font-size: var(--text-sm);
	}
	/* Live listening's controls, on a line of their own, measured: in one
	   row with their names beside their icons when there's room for all of
	   them, else with short names under their icons (below). */
	.bar {
		container: dock-controls / inline-size;
		flex: 1 1 100%;
		min-width: 0;
	}
	.controls {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-xs);
	}
	.controls button {
		gap: var(--space-xs);
	}
	/* Hold is on: the pressed button says so as well as the words beside the queue. */
	.controls button[aria-pressed='true'] {
		background: var(--accent-soft);
		border-color: var(--accent);
		color: var(--accent-soft-fg);
	}
	/* Catch up: words, by the queue count, only when behind. */
	.catch-up {
		min-height: var(--target);
		padding: 0 var(--space-xs);
		color: var(--accent);
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		text-decoration: underline;
	}
	.sep {
		color: var(--muted);
	}
	/* Previous: the skip icon, facing the other way. */
	.flip {
		display: inline-flex;
		transform: scaleX(-1);
	}
	.said {
		color: var(--fg);
	}
	.wide-only {
		display: contents;
	}
	/* What Share and Add to basket did shows above them, so the row never
	   grows or wraps while it shows. */
	.wide-only :global(.share),
	.wide-only :global(.add) {
		position: relative;
	}
	.wide-only :global(.done) {
		position: absolute;
		bottom: calc(100% + var(--space-2xs));
		/* Lined up with the button's end: Add to basket is the row's last,
		   at the dock's edge, and its words are wider than it. */
		right: 0;
		padding: var(--badge-pad-y) var(--badge-pad-x);
		border-radius: var(--radius-badge);
		background: var(--surface-2);
		color: var(--fg);
		white-space: nowrap;
	}
	.phone-only {
		display: none;
	}
	/* Not room for every control's name beside its icon in one row: each
	   shows a short name under its icon, and they share the row. Stop's
	   whole name is still read out. */
	/* The row with names beside the icons is about 60rem wide (with
	   Resume), so a desktop of 1,280 px beside the side menu has the short
	   names; the margin covers each browser's fonts. */
	@container dock-controls (width < 64rem) {
		.controls {
			flex-wrap: nowrap;
			gap: var(--space-2xs);
		}
		.controls > button,
		.wide-only :global(button),
		.dock-more :global(button.toggle) {
			flex: 1 1 0;
			flex-direction: column;
			gap: var(--space-3xs);
			min-width: 0;
			width: auto;
			padding: var(--space-2xs) var(--space-3xs);
			font-size: var(--text-xxs);
			font-weight: var(--weight-normal);
		}
		.wide-only :global(.share),
		.wide-only :global(.add) {
			display: flex;
			flex: 1 1 0;
			min-width: 0;
		}
		.wide-only :global(.share > button),
		.wide-only :global(.add > button) {
			width: 100%;
		}
		.long {
			position: absolute;
			width: var(--hidden-size);
			height: var(--hidden-size);
			margin: calc(-1 * var(--hidden-size));
			overflow: hidden;
			clip: rect(0, 0, 0, 0);
			white-space: nowrap;
		}
	}
	/* Phones: the controls across the width, short names under their icons;
	   Share and Add to basket in the Actions menu. */
	@media (max-width: 47.99rem) {
		.controls {
			flex: 1 1 100%;
			flex-wrap: nowrap;
			justify-content: space-between;
		}
		.wide-only {
			display: none;
		}
		.phone-only {
			display: flex;
			flex: 1 1 0;
			min-width: 0;
		}
		.dock-more :global(.more-actions),
		.dock-more :global(.has-tip) {
			display: flex;
			flex: 1 1 0;
			min-width: 0;
		}
		/* The dock is at the foot of the screen: the menu opens upwards. */
		.dock-more :global(.more-actions .panel.end) {
			top: auto;
			bottom: 100%;
			margin-top: 0;
			margin-bottom: var(--space-2xs);
		}
		/* The replay's Stop and Actions as live listening's controls are. */
		.replay .controls > button,
		.replay .dock-more :global(button.toggle) {
			flex: 1 1 0;
			flex-direction: column;
			gap: var(--space-3xs);
			min-width: 0;
			width: auto;
			padding: var(--space-2xs) var(--space-3xs);
			font-size: var(--text-xxs);
			font-weight: var(--weight-normal);
		}
		.replay .long {
			position: absolute;
			width: var(--hidden-size);
			height: var(--hidden-size);
			margin: calc(-1 * var(--hidden-size));
			overflow: hidden;
			clip: rect(0, 0, 0, 0);
			white-space: nowrap;
		}
		/* The replay above live listening, across the width. */
		.stack {
			display: flex;
			flex: 1 1 auto;
			flex-direction: column;
			align-items: stretch;
			gap: var(--space-xs);
			min-width: 0;
		}
		/* The replay: its badge, name and group beside its two controls; the
		   rest under both, across the width, wrapping between its parts. */
		.replay {
			position: relative;
			display: block;
		}
		.replay .np-name {
			min-height: var(--target);
			padding-right: calc(2 * var(--target) + 2 * var(--space-md) + 2 * var(--space-xs));
		}
		.replay-mark,
		.replay .led {
			flex: none;
		}
		.replay .tg {
			min-width: 0;
		}
		/* Beside the controls, the name and the group each move to the next
		   line whole when there's no room; a group longer than the line is
		   shortened with an ellipsis, a name wraps. Both are read out whole. */
		.replay .group {
			max-width: 100%;
			min-width: 0;
			overflow: hidden;
			text-overflow: ellipsis;
			white-space: nowrap;
		}
		.replay .np-unit > span {
			white-space: nowrap;
		}
		.replay .controls {
			position: absolute;
			top: 0;
			right: 0;
			flex: none;
			width: calc(2 * var(--target) + 2 * var(--space-md) + var(--space-xs));
		}
		/* Live listening under a replay: its status and queue in one small
		   line, then its controls. */
		.replaying .player {
			gap: var(--space-xs);
		}
		.replaying .queue {
			flex-basis: auto;
		}
	}
</style>
