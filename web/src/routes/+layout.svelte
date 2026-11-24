<script lang="ts">
	// ArchiveUx's tokens and base styles (R80), before every style of the
	// app's own. The app has no tokens of its own (ArchiveUx rule 1.6).
	import 'archiveux/archiveux.css';
	// ArchiveUx's frame (R81): the side menu on desktops, the bottom bar on
	// phones, and the account menu, signed out (listener sign-in is #61),
	// where the look and theme are chosen (R82). No app switcher until the
	// combined deployment (M7, R102), and no dock until the player (#60).
	import { AccountMenu, BottomBar, Frame, Menu } from 'archiveux/components';
	import { page } from '$app/state';
	import { current, setLook, setTheme, type Look, type Theme } from '#lib/look.ts';

	let { children } = $props();

	// The server sent the page in the saved look and theme; start from those.
	const sent = current();
	let look: Look = $state(sent.look);
	let theme: Theme = $state(sent.theme);
	// The account menu binds the theme; it's shown and saved when it changes.
	let shown = sent.theme;
	$effect(() => {
		if (theme === shown) return;
		shown = theme;
		setTheme(theme);
	});

	// R84's pages, as they arrive: Listen for now.
	const top = [{ href: '/', label: 'Listen', icon: 'play', text: 'Live calls from your listening set' }];
	const brand = { name: 'RadioArchive', mark: 'RA', href: '/' };
</script>

<Frame>
	{#snippet menu()}<Menu {brand} current={page.url.pathname} {top} />{/snippet}
	{#snippet start()}<a class="brand" href="/">RadioArchive</a>{/snippet}
	{#snippet account()}<AccountMenu account={null} bind:theme bind:look onlook={(l: Look) => setLook(l)} signIn="/signin" />{/snippet}
	{#snippet bottomBar()}<BottomBar entries={top} current={page.url.pathname} />{/snippet}
	{@render children()}
</Frame>

<style>
	/* The side menu carries the brand on desktops; phones have none, so the
	   top bar does. */
	.brand {
		display: none;
		color: var(--fg);
		font-size: var(--text-lg);
		font-weight: var(--weight-heavy);
		text-decoration: none;
	}
	@media (max-width: 47.99rem) {
		.brand {
			display: inline;
		}
	}
</style>
