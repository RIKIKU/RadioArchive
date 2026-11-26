<script lang="ts">
	// The dock's player (SPEC.md R44, R45, R81; ADR 0012), in ArchiveUx's
	// frame dock: Start listening, then now playing and the controls.
	//
	// Now playing: the LED colour mark beside the talkgroup's name (never the
	// only cue: the name is there), its group, the transmitting unit's alias
	// or ID (updated per transmission), the system only when there are
	// several, Live or Behind by N s or Delayed N min (ArchiveUx's badge),
	// the call's duration so far, and how many calls are queued. Hold shows
	// in words, beside the queue and on its pressed button.
	//
	// Controls (R44): Pause or Resume, Skip, Jump to live, Hold, Stop
	// listening. Each has its name in words: shown on a desktop, read out on
	// a phone, where the buttons show their icons only. Share (R45, ADR
	// 0017) copies the playing call's link; Add to basket (ADR 0018) puts it
	// in the download basket, live or not.
	//
	// A call replayed from recent calls (R46; ADR 0016) shows first, with
	// its own Stop and Share, whether live listening is paused or never
	// started. Several played in turn (Play all, play from here: ADR 0023)
	// show which call of them is playing, and when it started.
	//
	// On a phone (ADR 0023, "On a phone") the dock stacks: the replay first,
	// its name beside its controls and the rest under both, then
	// live listening, its status in a line above its controls. On a desktop
	// the two sit side by side, as before (the wrapper isn't drawn there).
	//
	// Proof of concept (poc/listening-set): Jump to live shows only when
	// behind, as "Catch up" in words beside the queue; on a phone each
	// control's short name shows under its icon, and Share and Add to
	// basket sit in a More menu.
	import { Icon, LiveBadge, MoreActions } from 'archiveux/components';
	import { LIVE_WITHIN } from 'archiveux/live.js';
	import type { Player, CallInfo } from './player.svelte.ts';
	import { dockIcons } from './icons.ts';
	import ShareButton from '#lib/share/ShareButton.svelte';
	import AddToBasket from '#lib/basket/AddToBasket.svelte';
	import { callLink, copyText } from '#lib/share/link.ts';
	import { basketState } from '#lib/basket/state.svelte.ts';

	let { player }: { player: Player } = $props();

	const basket = basketState();
	// Behind the radio: calls waiting, or a live call playing further back
	// than Live allows. A delayed talkgroup's call is behind on purpose.
	const behind = $derived(player.queued > 0 || (!!player.playing && !player.playing.delayMinutes && player.position.behind > LIVE_WITHIN));

	// What the More menu's actions did, beside the queue (the menu closes).
	let said = $state('');
	let saidTimer: ReturnType<typeof setTimeout> | undefined;
	function say(text: string) {
		clearTimeout(saidTimer);
		said = text;
		saidTimer = setTimeout(() => (said = ''), 4000);
	}
	async function shareCall(id: string) {
		say((await copyText(callLink(id))) ? 'Link copied' : "Couldn't copy the link");
	}
	function basketCall(id: string) {
		if (basket.has(id)) {
			basket.remove(id);
			say('Taken out of the basket');
		} else {
			basket.add(id);
			say(basket.has(id) ? 'Added to the basket' : 'The basket is full');
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
			</p>
		</div>
		<div class="controls" role="group" aria-label="Replay controls">
			<button type="button" class="secondary" onclick={() => player.replay.stop()}><Icon svg={dockIcons.stop} /><span class="label">Stop replay</span></button>
			<ShareButton callId={r.callId} label={r.talkgroup.name} icon compact />
			<AddToBasket callId={r.callId} label={r.talkgroup.name} icon compact />
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
		<div class="now">
			{#if c}
				<div class="what" aria-live="polite">
					<p class="np-name">
						{#if c.talkgroup.led_colour}<span class="led" data-led={c.talkgroup.led_colour} aria-hidden="true"></span>{/if}
						<span class="tg">{c.talkgroup.name}</span>{#if c.talkgroup.groups?.[0]}<span class="group">{c.talkgroup.groups[0]}</span>{/if}
					</p>
					<p class="meta np-unit">
						<span class="unit">{unitOf(c)}</span>{#if c.system}<span class="sep" aria-hidden="true">·</span><span class="system">{c.system.name}</span>{/if}
					</p>
				</div>
				<div class="where">
					{#if c.delayMinutes}
						<LiveBadge delay={c.delayMinutes} />
					{:else}
						<LiveBadge behind={player.position.behind} />
					{/if}
					<span class="meta elapsed"><span class="visually-hidden">{'Duration so far '}</span>{clock(player.position.elapsed)}</span>
				</div>
			{:else}
				<p class="status" aria-live="polite">
					{#if player.phase === 'starting' || !player.connected}
						Connecting…
					{:else if player.paused}
						Paused. Calls keep queuing.
					{:else if player.hold}
						Waiting for {player.hold.talkgroup.name}.
					{:else if player.last}
						Listening. Last call: {player.last.talkgroup.name}, {unitOf(player.last)}.
					{:else}
						Listening. Calls play here as they come in.
					{/if}
				</p>
			{/if}
			<p class="meta queue">
				<span class="queued">{queuedText}</span>
				{#if behind}<span class="sep" aria-hidden="true">·</span><button type="button" class="ghost catch-up" onclick={() => player.jumpToLive()}>Catch up<span class="visually-hidden"> to live</span></button>{/if}
				{#if player.hold}<span class="hold-on"><Icon svg={dockIcons.hold} />Hold on: {player.hold.talkgroup.name} only</span>{/if}
				{#if player.stepsBack}<span class="said" role="status">Back {player.stepsBack} {player.stepsBack === 1 ? 'call' : 'calls'}…</span>{/if}
				{#if said}<span class="said" role="status">{said}</span>{/if}
			</p>
		</div>
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
				><Icon svg={dockIcons.stop} /><span class="label"><span class="long">Stop listening</span><span class="short" aria-hidden="true">Stop</span></span></button
			>
			{#if c}
				<span class="wide-only"><ShareButton callId={c.callId} label={c.talkgroup.name} icon compact /><AddToBasket callId={c.callId} label={c.talkgroup.name} icon compact /></span>
				<span class="phone-only dock-more">
					<MoreActions label="More actions: {c.talkgroup.name}" text="Actions">
						<button type="button" onclick={() => shareCall(c.callId)}><Icon svg={dockIcons.share} />Share link</button>
						<button type="button" aria-pressed={basket.has(c.callId)} onclick={() => basketCall(c.callId)}
							><Icon svg={dockIcons.basket} />{basket.has(c.callId) ? 'Take out of basket' : 'Add to basket'}</button
						>
					</MoreActions>
				</span>
			{/if}
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
	.now {
		display: flex;
		flex: 1 1 var(--field-wide);
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2xs) var(--space-md);
		min-width: 0;
	}
	.now p {
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
	.where {
		display: flex;
		align-items: center;
		gap: var(--space-sm);
	}
	.elapsed {
		font-variant-numeric: tabular-nums;
	}
	.status {
		flex: 1 1 auto;
		padding: 0;
	}
	.queue {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2xs) var(--space-sm);
		flex-basis: 100%;
	}
	.hold-on {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2xs);
		padding: var(--badge-pad-y) var(--badge-pad-x);
		border-radius: var(--radius-badge);
		background: var(--accent-soft);
		color: var(--accent-soft-fg);
		font-weight: var(--weight-strong);
	}
	.meta {
		color: var(--muted);
		font-family: var(--font-meta);
		font-size: var(--text-sm);
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
	.phone-only,
	.short {
		display: none;
	}
	/* Phones: each control's short name under its icon. */
	@media (max-width: 47.99rem) {
		.controls {
			flex: 1 1 100%;
			justify-content: space-between;
		}
		.player .controls {
			flex-wrap: nowrap;
		}
		.player .controls > button,
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
		.player .long {
			position: absolute;
			width: var(--hidden-size);
			height: var(--hidden-size);
			margin: calc(-1 * var(--hidden-size));
			overflow: hidden;
			clip: rect(0, 0, 0, 0);
			white-space: nowrap;
		}
		.player .short {
			display: inline;
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
		.replay .controls button {
			width: var(--target);
			padding: 0;
		}
		.replay .controls .label {
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
		/* The replay: its badge, name and group beside its three controls; the
		   rest under both, across the width, wrapping between its parts. */
		.replay {
			position: relative;
			display: block;
		}
		.replay .np-name {
			min-height: var(--target);
			padding-right: calc(3 * var(--target) + 3 * var(--space-xs));
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
			justify-content: flex-end;
		}
		/* Live listening under a replay: its status and queue in one small
		   line, then its controls. */
		.replaying .player {
			gap: var(--space-xs);
		}
		.replaying .now {
			gap: 0 var(--space-sm);
		}
		.replaying .status {
			flex: 0 1 auto;
			font-size: var(--text-sm);
		}
		.replaying .queue {
			flex-basis: auto;
		}
	}
</style>
