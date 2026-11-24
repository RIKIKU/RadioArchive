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
	// a phone, where the buttons show their icons only. Share and Add to
	// basket (R45) are M3's.
	import { Icon, LiveBadge } from 'archiveux/components';
	import type { Player, CallInfo } from './player.svelte.ts';
	import { dockIcons } from './icons.ts';

	let { player }: { player: Player } = $props();

	const c = $derived(player.playing);
	const clock = (s: number) => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
	const unitOf = (i: CallInfo) => (i.unit ? i.unit.name || String(i.unit.id) : 'Unit unknown');
	const queuedText = $derived(
		player.queued === 0
			? 'No calls queued'
			: `${player.queued} queued${player.heldBack ? `, ${player.heldBack} after hold` : ''}`
	);
</script>

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
					<span class="meta elapsed"><span class="visually-hidden">Duration so far </span>{clock(player.position.elapsed)}</span>
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
				{#if player.hold}<span class="hold-on"><Icon svg={dockIcons.hold} />Hold on: {player.hold.talkgroup.name} only</span>{/if}
			</p>
		</div>
		<div class="controls" role="group" aria-label="Player controls">
			{#if player.paused}
				<button type="button" class="secondary" onclick={() => player.resume()}><Icon name="play" /><span class="label">Resume</span></button>
			{:else}
				<button type="button" class="secondary" onclick={() => player.pause()}><Icon name="pause" /><span class="label">Pause</span></button>
			{/if}
			<button type="button" class="secondary" disabled={!c} onclick={() => player.skip()}><Icon name="skip" /><span class="label">Skip</span></button>
			<button type="button" class="secondary" onclick={() => player.jumpToLive()}><Icon svg={dockIcons.live} /><span class="label">Jump to live</span></button>
			<button type="button" class="secondary" aria-pressed={!!player.hold} disabled={!c && !player.hold} onclick={() => player.setHold(!player.hold)}
				><Icon svg={dockIcons.hold} /><span class="label">Hold</span></button
			>
			{#if !c && player.last}
				{@const l = player.last}
				<button type="button" class="secondary" aria-label="Play again: {l.talkgroup.name}" onclick={() => player.playAgain(l)}
					><Icon svg={dockIcons.again} /><span class="label">Play again</span></button
				>
			{/if}
			<button type="button" class="secondary" onclick={() => player.stop()}><Icon svg={dockIcons.stop} /><span class="label">Stop listening</span></button>
		</div>
	</div>
{/if}

<style>
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
	/* Phones: the controls show their icons; their names are still read out. */
	@media (max-width: 47.99rem) {
		.controls {
			flex: 1 1 100%;
			justify-content: space-between;
		}
		.controls button {
			width: var(--target);
			padding: 0;
		}
		.controls .label {
			position: absolute;
			width: var(--hidden-size);
			height: var(--hidden-size);
			margin: calc(-1 * var(--hidden-size));
			overflow: hidden;
			clip: rect(0, 0, 0, 0);
			white-space: nowrap;
		}
	}
</style>
