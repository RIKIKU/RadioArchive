<script lang="ts">
	// A feed notice the listener can put away (SPEC.md R15; ADR 0050): the
	// recorder notice. It closes by its close button, by Escape while the
	// pointer is on it or the focus is in it, or by itself once it has been
	// on screen for `seconds` (10) without being touched, hovered or
	// focused. Touching, hovering or focusing it holds the count, and
	// leaving starts it again from the top (WCAG 2.2.1). A countdown line
	// along its foot shows the time left, as ArchiveUx's timed notice's
	// does, and waits with it. It doesn't fade, so reduced motion has
	// nothing to take away; the line stays, since it shows the time left.
	// It sits in the frame's notice place, whose status role announces it
	// once, as it appears (WCAG 4.1.3), and it never takes the focus.
	import { Icon } from 'archiveux/components';

	let {
		text,
		seconds = 10,
		ondismiss
	}: {
		text: string;
		seconds?: number;
		/** Closed by the listener, or timed out: both put it away. */
		ondismiss: (how: 'closed' | 'timed') => void;
	} = $props();

	let box: HTMLElement | undefined = $state();
	let hovered = $state(false);
	let focused = $state(false);
	const held = $derived(hovered || focused);
	// Each count, so the line starts again with it.
	let round = $state(0);

	$effect(() => {
		if (held) return;
		const t = setTimeout(() => ondismiss('timed'), seconds * 1000);
		return () => clearTimeout(t);
	});

	function release() {
		round++;
	}

	function close() {
		// The focus goes on to the page's content, where the next Tab would
		// have gone, rather than back to the top of the page.
		const main = document.getElementById('main');
		if (main) {
			if (!main.hasAttribute('tabindex')) {
				main.setAttribute('tabindex', '-1');
				main.addEventListener('blur', () => main.removeAttribute('tabindex'), { once: true });
			}
			main.focus({ preventScroll: true });
		}
		ondismiss('closed');
	}

	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'Escape' && held && !e.defaultPrevented) close();
	}
</script>

<svelte:window {onkeydown} />

<!-- The pointer and focus only hold the count: nothing here is a control
     but the close button, so the box has no role of its own. -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
	class="timed-notice"
	bind:this={box}
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
	<p>{text}</p>
	<span class="has-tip">
		<button type="button" class="ghost icon-button" aria-label="Dismiss: {text}" onclick={close}><Icon name="close" /></button>
		<span class="tooltip" aria-hidden="true">Dismiss</span>
	</span>
	{#key round}<span class="countdown" class:held style:animation-duration="{seconds}s" aria-hidden="true"></span>{/key}
</div>

<style>
	/* A notice's look (ArchiveUx section 4), with its close button at the
	   end of the line and the countdown line along its foot. */
	.timed-notice {
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
	}
	p {
		flex: 1 1 auto;
		min-width: 0;
		margin: 0;
	}
	.has-tip {
		flex: none;
	}
	/* Above the button: below it is the dock. */
	.has-tip > .tooltip {
		top: auto;
		bottom: calc(100% + var(--tip-gap));
		left: auto;
		right: 0;
		transform: none;
	}
	.icon-button {
		min-height: var(--target);
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
		animation: countdown 10s linear forwards;
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
