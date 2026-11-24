<script lang="ts">
	// TEMPORARY FRAME (issue #54, coordinator's default on #53 question 1).
	//
	// R81's frame is ArchiveUx's: brand, side menu, bottom bar, account menu
	// and the dock. ArchiveUx has no components yet (v0.2.1 is tokens and base
	// styles), so until it does this is the whole frame: the brand, the look
	// and theme choosers R82 needs, and the page. It lives only in this folder
	// and is used only by routes/+layout.svelte, so swapping in ArchiveUx's
	// frame and dock is one change: delete this folder and use theirs there.
	import type { Snippet } from 'svelte';
	import { onMount } from 'svelte';
	import { current, looks, setLook, setTheme, themes, type Look, type Theme } from '#lib/look.ts';

	let { children }: { children: Snippet } = $props();
	// The server sent the page in the saved look and theme; start from those.
	let look: Look = $state('harbour');
	let theme: Theme = $state('system');
	onMount(() => ({ look, theme } = current()));
</script>

<a class="skip" href="#main">Skip to content</a>
<header class="top">
	<a class="brand" href="/">RadioArchive</a>
	<div class="tools">
		<label class="choose">
			<span>Look</span>
			<select bind:value={look} onchange={() => setLook(look)} data-testid="look">
				{#each looks as l (l.value)}<option value={l.value}>{l.label}</option>{/each}
			</select>
		</label>
		<label class="choose">
			<span>Theme</span>
			<select bind:value={theme} onchange={() => setTheme(theme)} data-testid="theme">
				{#each themes as t (t.value)}<option value={t.value}>{t.label}</option>{/each}
			</select>
		</label>
	</div>
</header>
<main id="main">{@render children()}</main>

<style>
	.skip {
		position: absolute;
		left: 0;
		top: 0;
		transform: translateY(-200%);
	}
	.skip:focus {
		transform: translate(var(--space-sm), var(--space-sm));
		z-index: var(--layer-overlay);
		background: var(--surface-2);
		padding: var(--space-sm) var(--space-lg);
	}
	.top {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-sm) var(--space-lg);
		padding: var(--space-sm) var(--page-pad);
		background: var(--surface);
		border-bottom: var(--border-width) solid var(--border);
	}
	.brand {
		color: var(--fg);
		font-size: var(--text-lg);
		font-weight: var(--weight-heavy);
		text-decoration: none;
	}
	.tools {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-sm) var(--space-lg);
	}
	.choose {
		display: inline-flex;
		align-items: center;
		gap: var(--space-xs);
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.choose select {
		width: auto;
		color: var(--fg);
	}
	main {
		padding: var(--page-pad);
	}
	@media (max-width: 47.99rem) {
		.top,
		main {
			padding-left: var(--page-pad-phone);
			padding-right: var(--page-pad-phone);
		}
	}
</style>
