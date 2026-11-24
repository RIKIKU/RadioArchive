<script lang="ts">
	// Share (SPEC.md R45, R46, R61; ADR 0017): copies a call's link. On a
	// recent-calls row, on the call card and in now playing in the dock. Once
	// copied it says so beside the button, and to a screen reader. A browser
	// that won't copy gets the link in a field, selected, to copy by hand.
	import { tick } from 'svelte';
	import { Icon } from 'archiveux/components';
	import { dockIcons } from '#lib/audio/icons.ts';
	import { callLink, copyText } from './link.ts';

	let {
		callId,
		label,
		icon = false,
		compact = false
	}: {
		callId: string;
		/** The call as a person reads it ("Dispatch North, Wed 25 Nov, 14:03:22"), for the button's name. */
		label: string;
		/** Show the share icon before the word. */
		icon?: boolean;
		/** Phones: the icon alone, its name still read out (as the dock's controls). */
		compact?: boolean;
	} = $props();

	let shared: '' | 'copied' | 'failed' = $state('');
	let field: HTMLInputElement | undefined = $state();
	let timer: ReturnType<typeof setTimeout> | undefined;
	const link = $derived(callLink(callId));

	async function share() {
		clearTimeout(timer);
		if (await copyText(link)) {
			shared = 'copied';
			timer = setTimeout(() => (shared = ''), 4000);
			return;
		}
		shared = 'failed';
		await tick();
		field?.focus();
		field?.select();
	}
</script>

<span class="share" class:compact data-shared={shared || undefined}>
	<button type="button" class="secondary" aria-label="Share: {label}" onclick={share}>
		{#if icon}<Icon svg={dockIcons.share} />{/if}<span class="label">Share</span>
	</button>
	<span class="done" role="status">{shared === 'copied' ? 'Link copied' : ''}</span>
	{#if shared === 'failed'}
		<label class="by-hand">
			<span>Copy this link</span>
			<input bind:this={field} type="text" readonly value={link} onfocus={(e) => e.currentTarget.select()} />
		</label>
	{/if}
</span>

<style>
	.share {
		display: inline-flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2xs) var(--space-xs);
		min-width: 0;
	}
	button {
		gap: var(--space-xs);
	}
	.done {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.done:empty {
		display: none;
	}
	.by-hand {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2xs) var(--space-xs);
		flex-basis: 100%;
		font-size: var(--text-sm);
	}
	.by-hand input {
		flex: 1 1 auto;
		min-width: 0;
		max-width: var(--field-wide);
	}
	/* Phones, compact: the icon alone, as the dock's controls; the name and
	   "Link copied" are still read out. */
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
