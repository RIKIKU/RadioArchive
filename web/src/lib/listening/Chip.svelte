<script lang="ts">
	// One talkgroup as a chip (POC): its name, its label and a star when it's
	// priority (R43).
	//
	// - A tap or click on the chip switches it on or off (aria-pressed).
	// - A long press, about half a second, by touch or mouse, flips its
	//   priority mark instead: a buzz where the device has one, and the
	//   star pops. A long press never also switches the chip. The press is
	//   let go of when the pointer moves more than a few pixels, so a scroll
	//   that starts on a chip scrolls.
	// - The star is its own small toggle button ("Priority: <name>"), for
	//   the keyboard, screen readers and mouse users who'd rather click.
	//
	// No menus: the owner finds them hard to use on a phone.
	import type { Talkgroup } from './set.ts';

	let {
		talkgroup,
		on,
		priority,
		note,
		ontoggle,
		onpriority
	}: {
		talkgroup: Talkgroup;
		on: boolean;
		priority: boolean;
		/** A word under the name, such as "Added for this session". */
		note?: string;
		ontoggle: () => void;
		onpriority: () => void;
	} = $props();

	const HOLD_MS = 500;
	// How far the pointer may wander, in CSS pixels, before it's a scroll.
	const SLOP = 8;

	let timer: ReturnType<typeof setTimeout> | undefined;
	let x0 = 0;
	let y0 = 0;
	// The press that just ended was a long one: its click is swallowed.
	let held = false;
	let pressing = $state(false);
	let flash = $state(false);
	let flashTimer: ReturnType<typeof setTimeout> | undefined;

	const label = $derived(talkgroup.label && talkgroup.label !== talkgroup.name ? talkgroup.label : '');

	function confirm() {
		flash = false;
		clearTimeout(flashTimer);
		// A frame later, so a second long press replays the pop.
		requestAnimationFrame(() => {
			flash = true;
			flashTimer = setTimeout(() => (flash = false), 700);
		});
	}

	function cancel() {
		clearTimeout(timer);
		timer = undefined;
		pressing = false;
	}

	function down(e: PointerEvent) {
		if (e.button !== 0) return;
		held = false;
		x0 = e.clientX;
		y0 = e.clientY;
		pressing = true;
		clearTimeout(timer);
		timer = setTimeout(() => {
			timer = undefined;
			pressing = false;
			held = true;
			try {
				navigator.vibrate?.(30);
			} catch {
				// No buzz: the star's pop is the confirmation.
			}
			onpriority();
			confirm();
		}, HOLD_MS);
	}

	function move(e: PointerEvent) {
		if (timer === undefined) return;
		if (Math.abs(e.clientX - x0) > SLOP || Math.abs(e.clientY - y0) > SLOP) cancel();
	}

	function click(e: MouseEvent) {
		// A keyboard's click (detail 0) is never a long press's.
		if (held && e.detail !== 0) {
			held = false;
			e.preventDefault();
			return;
		}
		held = false;
		ontoggle();
	}

	function star() {
		onpriority();
		confirm();
	}
</script>

<div class="chip" data-on={on} data-priority={priority} data-flash={flash || undefined}>
	<button
		type="button"
		class="main"
		aria-pressed={on ? 'true' : 'false'}
		data-pressing={pressing || undefined}
		onpointerdown={down}
		onpointermove={move}
		onpointerup={cancel}
		onpointercancel={cancel}
		onpointerleave={cancel}
		oncontextmenu={(e) => e.preventDefault()}
		onclick={click}
	>
		<span class="mark" aria-hidden="true"></span>
		<span class="what">
			<span class="name">{talkgroup.name}</span>
			<!-- Many talkgroups share a name, so the label, or else the number, always shows. -->
			<span class="meta">
				<span>{label || `TG ${talkgroup.id}`}</span>
				{#if !on}<span class="off">Off</span>{/if}
				{#if note}<span>{note}</span>{/if}
			</span>
		</span>
	</button>
	<button type="button" class="star" aria-pressed={priority ? 'true' : 'false'} aria-label="Priority: {talkgroup.name}" title={priority ? 'Priority (click to take off)' : 'Make priority'} onclick={star}
		><span aria-hidden="true">{priority ? '★' : '☆'}</span></button
	>
</div>

<style>
	.chip {
		position: relative;
		display: flex;
		align-items: stretch;
		min-width: 0;
		background: var(--surface-2);
		border: var(--border-width) solid var(--control-border);
		border-radius: var(--radius-button);
		-webkit-touch-callout: none;
		user-select: none;
		-webkit-user-select: none;
	}
	.chip[data-on='true'] {
		background: var(--accent-soft);
		border-color: var(--accent);
		color: var(--accent-soft-fg);
	}
	/* Off, but left in place so a second tap puts it back. */
	.chip[data-on='false'] {
		border-style: dashed;
		color: var(--muted);
	}
	.chip[data-flash] {
		animation: confirm 0.6s ease-out;
	}
	.chip button {
		min-height: var(--target);
		border: 0;
		border-radius: var(--radius-button);
		background: transparent;
		color: inherit;
	}
	/* Hover only where there's a pointer that hovers: a tap leaves it stuck on a phone. */
	@media (hover: hover) {
		.chip button:hover {
			background: var(--hover);
		}
	}
	.chip button:active {
		background: transparent;
	}
	.main {
		position: relative;
		flex: 1 1 auto;
		justify-content: flex-start;
		gap: var(--space-xs);
		min-width: 0;
		padding: var(--space-2xs) var(--space-2xs) var(--space-2xs) var(--space-sm);
		text-align: left;
		font-weight: var(--weight-normal);
		overflow: hidden;
		/* Scrolls and pans still work; the double-tap zoom wait goes. */
		touch-action: manipulation;
	}
	/* The hold filling, so a long press shows it's coming. */
	.main[data-pressing]::after {
		content: '';
		position: absolute;
		left: 0;
		bottom: 0;
		height: var(--border-width-strong);
		background: var(--accent);
		animation: hold 0.5s linear forwards;
	}
	.what {
		display: flex;
		flex-direction: column;
		min-width: 0;
	}
	.name {
		font-size: var(--text-sm);
		font-weight: var(--weight-strong);
		line-height: var(--leading-tight);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.meta {
		display: flex;
		gap: var(--space-xs);
		color: var(--muted);
		font-family: var(--font-meta);
		font-size: var(--text-xxs);
		overflow: hidden;
		white-space: nowrap;
		text-overflow: ellipsis;
	}
	.chip[data-on='true'] .meta {
		color: var(--accent-soft-fg);
	}
	.off {
		font-weight: var(--weight-strong);
	}
	.mark {
		flex: none;
		width: var(--badge-dot-size);
		height: var(--badge-dot-size);
		border: var(--border-width-strong) solid currentColor;
		border-radius: var(--radius-round);
	}
	.chip[data-on='true'] .mark {
		background: currentColor;
	}
	.star {
		flex: none;
		min-width: var(--count-min);
		padding: 0 var(--space-2xs);
		font-size: var(--text-md);
		line-height: var(--leading-tight);
	}
	.star[aria-pressed='false'] {
		opacity: 0.45;
	}
	.star[aria-pressed='false']:hover,
	.star[aria-pressed='false']:focus-visible {
		opacity: 1;
	}
	.chip[data-flash] .star span {
		display: inline-block;
		animation: pop 0.6s ease-out;
	}
	@keyframes hold {
		from {
			width: 0%;
		}
		to {
			width: 100%;
		}
	}
	@keyframes pop {
		0% {
			transform: scale(1);
		}
		35% {
			transform: scale(1.8);
		}
		100% {
			transform: scale(1);
		}
	}
	@keyframes confirm {
		0% {
			box-shadow: 0 0 0 var(--focus-width) var(--focus);
		}
		100% {
			box-shadow: 0 0 0 0 transparent;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.chip[data-flash],
		.chip[data-flash] .star span,
		.main[data-pressing]::after {
			animation-duration: 0.01s;
		}
	}
</style>
