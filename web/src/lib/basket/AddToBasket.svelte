<script lang="ts">
	// Add to basket (SPEC.md R45, R46, R64; ADR 0018): puts a call in the
	// download basket, beside Share, on a recent-calls row, on the call card
	// and in now playing in the dock. A live call goes in at once and can be
	// downloaded once its recording has arrived. The button is pressed while
	// the call is in the basket, and pressing it again takes the call out;
	// either way it says so beside the button, and to a screen reader.
	import { Icon } from 'archiveux/components';
	import { dockIcons } from '#lib/audio/icons.ts';
	import { basketState } from './state.svelte.ts';

	let {
		callId,
		label,
		icon = false,
		compact = false
	}: {
		callId: string;
		/** The call as a person reads it ("Dispatch North, Wed 25 Nov, 14:03:22"), for the button's name. */
		label: string;
		/** Show the basket icon before the words. */
		icon?: boolean;
		/** Phones: the icon alone, its name still read out (as the dock's controls). */
		compact?: boolean;
	} = $props();

	const basket = basketState();
	const inBasket = $derived(basket.has(callId));
	let said = $state('');
	let timer: ReturnType<typeof setTimeout> | undefined;

	function press() {
		clearTimeout(timer);
		if (inBasket) {
			basket.remove(callId);
			said = 'Taken out of the basket';
		} else {
			basket.add(callId);
			said = basket.has(callId) ? 'Added to the basket' : 'The basket is full';
		}
		timer = setTimeout(() => (said = ''), 4000);
	}
</script>

<span class="add" class:compact>
	<button type="button" class="secondary" aria-pressed={inBasket} aria-label="Add to basket: {label}" onclick={press}>
		{#if icon}<Icon svg={dockIcons.basket} />{/if}<span class="label">Add to basket</span>
	</button>
	<span class="done" role="status">{said}</span>
</span>

<style>
	.add {
		display: inline-flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2xs) var(--space-xs);
		min-width: 0;
	}
	button {
		gap: var(--space-xs);
	}
	/* In the basket: pressed, as the dock's Hold shows it. */
	button[aria-pressed='true'] {
		background: var(--accent-soft);
		border-color: var(--accent);
		color: var(--accent-soft-fg);
	}
	.done {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.done:empty {
		display: none;
	}
	/* Phones, compact: the icon alone, as the dock's controls; the name and
	   what happened are still read out. */
	@media (max-width: 47.99rem) {
		.compact button {
			width: var(--target);
			padding: 0;
		}
		.compact .label,
		.compact .done {
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
