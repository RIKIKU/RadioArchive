<script lang="ts">
	// A download link that checks first (ADR 0038, 0039): the browser's
	// download manager can't show why a download was refused, so a click
	// asks the server for the file's first byte, which it answers as it
	// would the download. Refused over a limit, the link stays and the
	// server's message shows under it ("Too many downloads at once: at most
	// 2 from here. Try again in 2 minutes."); otherwise the download starts.
	import type { Snippet } from 'svelte';
	import { downloadRefusal } from '#lib/limits.ts';

	let {
		href,
		class: cls = 'button secondary',
		label,
		ondownload,
		children
	}: {
		href: string;
		class?: string;
		/** The link's accessible name, when its text isn't enough. */
		label?: string;
		/** The download has started. */
		ondownload?: () => void;
		children: Snippet;
	} = $props();

	let link: HTMLAnchorElement | undefined = $state();
	let refused = $state('');
	let checking = $state(false);
	// The click the check lets through, made by the check itself.
	let through = false;

	async function click(e: MouseEvent) {
		if (through) {
			through = false;
			ondownload?.();
			return;
		}
		e.preventDefault();
		if (checking) return;
		checking = true;
		const l = await downloadRefusal(href);
		checking = false;
		refused = l?.message ?? '';
		if (l) return;
		through = true;
		link?.click();
	}
</script>

<a bind:this={link} class={cls} {href} download aria-label={label} aria-busy={checking || undefined} onclick={click}>{@render children()}</a>
{#if refused}<p class="error download-refused" role="alert">{refused}</p>{/if}

<style>
	.download-refused {
		flex-basis: 100%;
		margin: var(--space-2xs) 0 0;
	}
</style>
