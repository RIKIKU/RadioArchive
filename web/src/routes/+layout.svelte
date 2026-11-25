<script lang="ts">
	// ArchiveUx's tokens and base styles (R80), before every style of the
	// app's own. The app has no tokens of its own (ArchiveUx rule 1.6).
	import 'archiveux/archiveux.css';
	// ArchiveUx's frame (R81): the side menu on desktops, the bottom bar on
	// phones, and the account menu, where the look and theme are chosen
	// (R82) signed in or out, and where a listener signs in to save their
	// preferences (R103, R110; ADR 0009). No app switcher until the combined
	// deployment (M7, R102). The dock holds the player (R44, R45, R81; ADR
	// 0012) on every page, and the live feed's notices sit above it.
	import { AccountMenu, BottomBar, Frame, Menu } from 'archiveux/components';
	import { onDestroy, onMount } from 'svelte';
	import { Player } from '#lib/audio/player.svelte.ts';
	import Dock from '#lib/audio/Dock.svelte';
	import { providePlayer } from '#lib/audio/context.ts';
	import { keyOf } from '#lib/listening/set.ts';
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { current, setLook, setTheme, type Look, type Theme } from '#lib/look.ts';
	import { alertsOn, setAlerts } from '#lib/playback.ts';
	import { listeningState } from '#lib/listening/state.svelte.ts';
	import { indexOf, resolve, type Catalogue } from '#lib/listening/set.ts';
	import { Account } from '#lib/account/account.svelte.ts';
	import { basketState } from '#lib/basket/state.svelte.ts';
	import { dockIcons } from '#lib/audio/icons.ts';
	import type { Profile } from '#lib/account/profile.ts';
	import { adminGroup } from '#lib/admin/menu.ts';
	import { tryingAgain } from '#lib/limits.ts';

	let { children } = $props();

	// The server sent the page in the saved look and theme; start from those.
	const sent = current();
	let look: Look = $state(sent.look);
	let theme: Theme = $state(sent.theme);
	// Playback settings (R48): alert sounds, off by default.
	let alerts = $state(alertsOn());

	const listening = listeningState();
	// The download basket (R64; ADR 0018), saved with the set (R110).
	const basket = basketState();

	// The player, in the dock. Priority marks are the saved set's (R43); the
	// talkgroups added for the session have none.
	const player = new Player({
		isPriority: (t) => listening.saved.some((e) => e.priority && keyOf(e) === keyOf(t)),
		alerts: () => alerts
	});
	onDestroy(() => {
		player.stop();
		player.replay.stop();
	});
	providePlayer(player);

	function chooseAlerts(on: boolean) {
		alerts = on;
		setAlerts(on);
		acct.changed();
	}
	const acct = new Account({
		listening,
		basket,
		look: () => look,
		theme: () => theme,
		alerts: () => alerts,
		apply(p: Profile) {
			look = p.look ?? 'harbour';
			setLook(look);
			theme = p.theme ?? 'system';
			shown = theme;
			setTheme(theme);
			alerts = p.alerts;
			setAlerts(alerts);
		}
	});

	// The account menu binds the theme; it's shown and saved when it changes.
	let shown = sent.theme;
	$effect(() => {
		if (theme === shown) return;
		shown = theme;
		setTheme(theme);
		acct.changed();
	});
	function chooseLook(l: Look) {
		setLook(l);
		acct.changed();
	}

	// What a sign-in or sign-out came back to say, once.
	type Notice = 'signin-failed' | 'signin-unavailable' | 'signin-disabled' | 'signed-out' | 'signed-out-here';
	let notice: Notice | undefined = $state();
	function readNotice() {
		const url = new URL(page.url.href);
		const p = url.searchParams;
		if (p.get('signin') === 'failed') notice = 'signin-failed';
		else if (p.get('signin') === 'unavailable') notice = 'signin-unavailable';
		else if (p.get('signin') === 'disabled') notice = 'signin-disabled';
		else if (p.get('signed_out') === '1') notice = p.get('authentik') === 'unreachable' ? 'signed-out-here' : 'signed-out';
		if (!notice) return;
		for (const k of ['signin', 'signed_out', 'authentik']) p.delete(k);
		try {
			replaceState(url, page.state);
		} catch {
			// The router isn't ready: the address keeps it.
		}
	}

	// The prompt's names: the account's set, as the catalogue shows it.
	let offered: string[] = $state([]);
	$effect(() => {
		const offer = acct.offer;
		if (!offer) return;
		fetch('/api/v1/catalogue', { headers: { Accept: 'application/json' } })
			.then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
			.then((c: Catalogue) => (offered = resolve(indexOf(c), offer.talkgroups).map((r) => r.talkgroup.name)))
			.catch(() => (offered = []));
	});

	onMount(() => {
		readNotice();
		void acct.load();
	});

	const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? '' : 's'}`;
	const signInHref = $derived(`/auth/signin?return=${encodeURIComponent(page.url.pathname + page.url.search)}`);
	const who = $derived(acct.who ? { name: acct.who.name, email: acct.who.email } : null);

	// R84's pages, as they arrive: Listen, Recent calls and Basket, with how
	// many calls the basket holds as a quiet pill. The phone's bottom bar
	// says Recent.
	const held = $derived(basket.basket.calls.length);
	const top = $derived([
		{ href: '/', label: 'Listen', icon: 'play', text: 'Live calls from your listening set' },
		{ href: '/recent', label: 'Recent calls', icon: 'clock', text: 'Search and replay the archive' },
		{ href: '/basket', label: 'Basket', svg: dockIcons.basket, text: 'Calls to download', count: held, quiet: true, says: held ? `, ${held} ${held === 1 ? 'call' : 'calls'}` : undefined }
	]);
	const bar = $derived([top[0], { ...top[1], label: 'Recent' }, top[2]]);
	const brand = { name: 'RadioArchive', mark: 'RA', href: '/' };

	// R84's Admin section (ADR 0027), for an admin only: a group of the side
	// menu, and on phones a group of the bottom bar's More. It opens
	// nothing by itself: each admin page follows the admin API's answer.
	const groups = $derived(acct.who?.admin ? [adminGroup()] : []);
	let opened: Record<string, boolean> = $state({});
</script>

<Frame dockLabel="Now playing">
	{#snippet menu()}<Menu {brand} current={page.url.pathname} {top} {groups} open={opened} ontoggle={(id, was) => (opened = { ...opened, [id]: !was })} />{/snippet}
	{#snippet start()}<a class="brand" href="/">RadioArchive</a>{/snippet}
	{#snippet dock()}<Dock {player} />{/snippet}
	{#snippet notices()}
		<!-- The live feed's notices (R15, R81), above the dock, while they're on. -->
		<div class="notice-place" aria-live="polite">
			{#if player.notices.recorders}<p class="feed-notice">Some talkgroups aren't being received right now.</p>{/if}
			<!-- A refused socket (R71; ADR 0037, 0039): the feed tries again by itself. -->
			{#if player.notices.radio_full}<p class="feed-notice limit">The radio is full right now. Trying again…</p>{/if}
			{#if player.notices.too_many_live}<p class="feed-notice limit">{tryingAgain(player.liveLimit)}</p>{/if}
		</div>
	{/snippet}
	{#snippet account()}
		<AccountMenu account={who} bind:theme bind:look onlook={chooseLook} signIn={signInHref} onsignout={() => acct.signOut()}>
			{#snippet extra({ close }: { close: () => void })}
				<!-- Playback settings (R48), with the look (R82), signed in or out. -->
				<label class="setting">
					<input type="checkbox" checked={alerts} onchange={(e) => chooseAlerts(e.currentTarget.checked)} />
					<span>Alert sounds<span class="setting-hint">A talkgroup's sound when a call starts on it</span></span>
				</label>
				{#if acct.who}
					<p class="saved" role="status">
						{#if acct.mode === 'saving'}
							{acct.saveFailed ? "Couldn't save to your account. It tries again at your next change." : 'Your listening set, basket and settings are saved to your account.'}
						{:else if acct.mode === 'asking'}
							Choose which listening set to keep.
						{:else if acct.mode === 'not-saved'}
							This account's settings aren't saved here.
						{:else if acct.mode === 'unavailable'}
							Couldn't reach your account just now.
						{/if}
					</p>
					{#if acct.mode === 'saving'}
						<button type="button" class="ghost clear" onclick={() => (close(), (acct.clearing = 'confirm'))}>Clear saved settings</button>
					{/if}
				{/if}
			{/snippet}
		</AccountMenu>
	{/snippet}
	{#snippet bottomBar()}<BottomBar entries={bar} {groups} current={page.url.pathname} />{/snippet}

	{#if notice === 'signin-failed'}
		<p class="alert" role="alert">Couldn't sign you in. Try again from the account menu. Listening works as before.</p>
	{:else if notice === 'signin-disabled'}
		<p class="alert" role="alert">This account has been turned off here, so you weren't signed in. Listening works as before.</p>
	{:else if notice === 'signin-unavailable'}
		<p class="alert" role="alert">Signing in isn't available right now. Listening works as before.</p>
	{:else if notice === 'signed-out'}
		<p class="ok" role="status">You're signed out. This browser keeps its listening set.</p>
	{:else if notice === 'signed-out-here'}
		<p class="alert" role="status">You're signed out of the radio, but the sign-in service couldn't be reached to sign you out there too.</p>
	{/if}

	{#if acct.mode === 'asking' && acct.offer}
		<section class="panel" aria-labelledby="account-offer-heading">
			<h2 id="account-offer-heading">Use the listening set saved to your account?</h2>
			<p>
				Your account has {plural(offered.length || acct.offer.talkgroups.length, 'talkgroup')}{#if acct.offer.talkgroups.some((e) => e.priority)}, with priority marks{/if}{#if acct.offer.basket?.calls.length}, and {plural(acct.offer.basket.calls.length, 'call')} in its basket{/if}.
				This browser has {plural(listening.saved.length, 'talkgroup')}{#if basket.basket.calls.length}, and {plural(basket.basket.calls.length, 'call')} in its basket{/if}. Nothing is merged: choose one.
			</p>
			{#if offered.length}
				<ul class="names" aria-label="Your account's listening set">
					{#each offered as name, i (i)}<li>{name}</li>{/each}
				</ul>
			{/if}
			<div class="actions">
				<button type="button" onclick={() => acct.useAccounts()}>Use my account's set</button>
				<button type="button" class="secondary" onclick={() => acct.keepBrowsers()}>Keep this browser's set</button>
			</div>
			<p class="hint">Keeping this browser's set saves it to your account in place of the one there. Either way, the look, the playback settings and the download basket go with the set.</p>
		</section>
	{/if}

	{#if acct.clearing === 'confirm'}
		<section class="panel" aria-labelledby="account-clear-heading">
			<h2 id="account-clear-heading">Clear what your account holds?</h2>
			<p>Your listening set, priority marks, playback settings, look and download basket are removed from your account. This browser keeps its own.</p>
			<div class="actions">
				<button type="button" class="danger" onclick={() => acct.clear()}>Clear my account's settings</button>
				<button type="button" class="secondary" onclick={() => (acct.clearing = '')}>Cancel</button>
			</div>
		</section>
	{:else if acct.clearing === 'cleared'}
		<p class="ok" role="status">Your account holds nothing now. This browser keeps its listening set; a change you make while signed in is saved to your account again.</p>
	{:else if acct.clearing === 'failed'}
		<p class="alert" role="alert">Couldn't clear your account just now. Try again in a minute.</p>
	{/if}

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
	/* As the menu's own rows. */
	.setting {
		display: flex;
		align-items: flex-start;
		gap: var(--space-sm);
		min-height: var(--target);
		padding: var(--space-xs) var(--space-md);
		font-size: var(--text-sm);
		cursor: pointer;
	}
	.setting-hint {
		display: block;
		color: var(--muted);
	}
	/* A feed notice: a notice's look (ArchiveUx section 4), placed by
	   .notice-place. It stays while it's on, so it has no timing. */
	.feed-notice {
		margin: 0;
		padding: var(--space-sm) var(--space-md);
		background: var(--highlight);
		color: var(--fg);
		border: var(--border-width-strong) solid var(--notice-edge);
		border-radius: var(--radius);
		box-shadow: var(--notice-shadow);
		font-size: var(--text-sm);
	}
	.saved {
		margin: 0;
		padding: var(--space-xs) var(--space-md);
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.saved:empty {
		display: none;
	}
	/* As the menu's own rows. */
	.clear {
		justify-content: flex-start;
		width: 100%;
		padding: 0 var(--space-md);
		font-weight: var(--weight-normal);
	}
	.alert {
		margin: 0 0 var(--space-md);
		padding: var(--space-sm) var(--space-md);
		background: var(--warn-bg);
		color: var(--warn-fg);
		border-radius: var(--radius);
	}
	.ok {
		margin: 0 0 var(--space-md);
	}
	.panel {
		margin: 0 0 var(--space-xl);
		padding: var(--space-lg);
		background: var(--surface);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
	}
	.panel h2 {
		margin-top: 0;
	}
	.names {
		margin: var(--space-sm) 0 var(--space-lg);
		padding-left: var(--space-xl);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-sm);
	}
	.hint {
		margin-bottom: 0;
		color: var(--muted);
		font-size: var(--text-sm);
	}
</style>
