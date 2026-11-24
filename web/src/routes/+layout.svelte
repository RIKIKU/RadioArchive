<script lang="ts">
	// ArchiveUx's tokens and base styles (R80), before every style of the
	// app's own. The app has no tokens of its own (ArchiveUx rule 1.6).
	import 'archiveux/archiveux.css';
	// ArchiveUx's frame (R81): the side menu on desktops, the bottom bar on
	// phones, and the account menu, where the look and theme are chosen
	// (R82) signed in or out, and where a listener signs in to save their
	// preferences (R103, R110; ADR 0009). No app switcher until the combined
	// deployment (M7, R102), and no dock until the player (#60).
	import { AccountMenu, BottomBar, Frame, Menu } from 'archiveux/components';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { current, setLook, setTheme, type Look, type Theme } from '#lib/look.ts';
	import { alertsOn, setAlerts } from '#lib/playback.ts';
	import { listeningState } from '#lib/listening/state.svelte.ts';
	import { indexOf, resolve, type Catalogue } from '#lib/listening/set.ts';
	import { Account } from '#lib/account/account.svelte.ts';
	import type { Profile } from '#lib/account/profile.ts';

	let { children } = $props();

	// The server sent the page in the saved look and theme; start from those.
	const sent = current();
	let look: Look = $state(sent.look);
	let theme: Theme = $state(sent.theme);
	let alerts = alertsOn();

	const listening = listeningState();
	const acct = new Account({
		listening,
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
	type Notice = 'signin-failed' | 'signin-unavailable' | 'signed-out' | 'signed-out-here';
	let notice: Notice | undefined = $state();
	function readNotice() {
		const url = new URL(page.url.href);
		const p = url.searchParams;
		if (p.get('signin') === 'failed') notice = 'signin-failed';
		else if (p.get('signin') === 'unavailable') notice = 'signin-unavailable';
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

	// R84's pages, as they arrive: Listen for now.
	const top = [{ href: '/', label: 'Listen', icon: 'play', text: 'Live calls from your listening set' }];
	const brand = { name: 'RadioArchive', mark: 'RA', href: '/' };
</script>

<Frame>
	{#snippet menu()}<Menu {brand} current={page.url.pathname} {top} />{/snippet}
	{#snippet start()}<a class="brand" href="/">RadioArchive</a>{/snippet}
	{#snippet account()}
		<AccountMenu account={who} bind:theme bind:look onlook={chooseLook} signIn={signInHref} onsignout={() => acct.signOut()}>
			{#snippet extra({ close }: { close: () => void })}
				{#if acct.who}
					<p class="saved" role="status">
						{#if acct.mode === 'saving'}
							{acct.saveFailed ? "Couldn't save to your account. It tries again at your next change." : 'Your listening set and settings are saved to your account.'}
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
	{#snippet bottomBar()}<BottomBar entries={top} current={page.url.pathname} />{/snippet}

	{#if notice === 'signin-failed'}
		<p class="alert" role="alert">Couldn't sign you in. Try again from the account menu. Listening works as before.</p>
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
				Your account has {plural(offered.length || acct.offer.talkgroups.length, 'talkgroup')}{#if acct.offer.talkgroups.some((e) => e.priority)}, with priority marks{/if}.
				This browser has {plural(listening.saved.length, 'talkgroup')}. Nothing is merged: choose one.
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
			<p class="hint">Keeping this browser's set saves it to your account in place of the one there. Either way, the look and playback settings go with the set.</p>
		</section>
	{/if}

	{#if acct.clearing === 'confirm'}
		<section class="panel" aria-labelledby="account-clear-heading">
			<h2 id="account-clear-heading">Clear what your account holds?</h2>
			<p>Your listening set, priority marks, playback settings and look are removed from your account. This browser keeps its own.</p>
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
