<script lang="ts">
	// Avoid's toast (ADR 0055): "Avoided <talkgroup> · Undo", in the frame's
	// notice place above the dock, whose status role announces it politely,
	// once, as it appears; it never takes the focus. It follows ArchiveUx's
	// timing for a notice that leaves by itself (section 4), but stays
	// AVOID_SECONDS (the owner's "about 6 s") rather than 8: a countdown line
	// along its foot shows the time left; touching, hovering or focusing it
	// holds the count, and letting go starts it again; a tap or
	// click anywhere on it but Undo closes it, and Escape closes it from
	// anywhere. There's no close button. It doesn't fade, so reduced motion
	// has nothing to take away; the line stays, since it shows the time left.
	import { AVOID_SECONDS } from './avoid.svelte.ts';

	let { name, onundo, ondismiss }: { name: string; onundo: () => void; ondismiss: () => void } = $props();

	let box: HTMLElement | undefined = $state();
	let hovered = $state(false);
	let focused = $state(false);
	const held = $derived(hovered || focused);
	// Each count, so the line starts again with it: once let go, the count
	// starts again from the top, as the recorder notice's does (ADR 0050),
	// which is more than ArchiveUx's second at least.
	let round = $state(0);

	$effect(() => {
		if (held) return;
		const t = setTimeout(ondismiss, AVOID_SECONDS * 1000);
		return () => clearTimeout(t);
	});

	function release() {
		round++;
	}

	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'Escape' && !e.defaultPrevented) ondismiss();
	}

	// A tap on the toast closes it; Undo's own click undoes instead.
	function onclick(e: MouseEvent) {
		if ((e.target as Element | null)?.closest('button')) return;
		ondismiss();
	}
</script>

<svelte:window {onkeydown} />

<!-- A tap anywhere on it closes it, as ArchiveUx's toasts do; Escape is the
     keyboard's way, so the box needs no role or key handler of its own. -->
<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div
	class="avoid-toast"
	bind:this={box}
	{onclick}
	onpointerenter={() => (hovered = true)}
	onpointerleave={() => {
		hovered = false;
		release();
	}}
	onfocusin={() => (focused = true)}
	onfocusout={(e) => {
		if (box?.contains(e.relatedTarget as Node | null)) return;
		focused = false;
		release();
	}}
>
	<p>Avoided <span class="tg">{name}</span><span class="sep" aria-hidden="true">·</span></p>
	<button type="button" class="ghost undo" aria-label="Undo: turn {name} back on" onclick={onundo}>Undo</button>
	{#key round}<span class="countdown" class:held style:animation-duration="{AVOID_SECONDS}s" aria-hidden="true"></span>{/key}
</div>

<style>
	/* A notice's look (ArchiveUx section 4), Undo at the end of its line
	   and the countdown line along its foot. */
	.avoid-toast {
		position: relative;
		display: flex;
		align-items: center;
		gap: var(--space-xs);
		padding: var(--space-3xs) var(--space-3xs) var(--space-3xs) var(--space-md);
		background: var(--highlight);
		color: var(--fg);
		border: var(--border-width-strong) solid var(--notice-edge);
		border-radius: var(--radius);
		box-shadow: var(--notice-shadow);
		font-size: var(--text-sm);
		cursor: default;
	}
	p {
		flex: 1 1 auto;
		min-width: 0;
		margin: 0;
		overflow-wrap: anywhere;
	}
	.tg {
		font-weight: var(--weight-strong);
	}
	.sep {
		margin-left: var(--space-xs);
		color: var(--muted);
	}
	.undo {
		flex: none;
		min-height: var(--target);
		color: var(--accent);
		font-weight: var(--weight-strong);
	}
	.countdown {
		position: absolute;
		left: 0;
		right: 0;
		bottom: 0;
		height: var(--notice-countdown-height);
		background: var(--notice-edge);
		border-radius: 0 0 var(--radius-small) var(--radius-small);
		transform-origin: left;
		animation: countdown 6s linear forwards;
	}
	.countdown.held {
		animation-play-state: paused;
	}
	@keyframes countdown {
		to {
			transform: scaleX(0);
		}
	}
</style>
