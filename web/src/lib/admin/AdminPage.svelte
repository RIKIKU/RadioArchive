<script lang="ts">
	// The frame of every admin page (SPEC.md R84, R88; ADR 0027), inside
	// ArchiveUx's frame: the page's heading, the recordings volume's warning,
	// and the page itself once the admin API has answered it as an admin's.
	//
	// The admin API is the guard (ADR 0025): access comes from the answer to
	// the page's own data. Signed out, the page offers sign-in; signed in
	// but not an admin, it says "This page is for admins" and nothing else.
	//
	// The disk warning (R88: "a warning shows to admins at 15 % free") is
	// GET /admin/retention's volume state, as an alert that stays (ArchiveUx
	// section 4, "Alerts"), on every admin page and on no listener page.
	import type { Snippet } from 'svelte';
	import { onDestroy } from 'svelte';
	import { page } from '$app/state';
	import { admin, diskWarning, type Access, type Retention } from './api.ts';

	/** How often the volume's state is asked again. */
	const DISK_MS = 60_000;

	let { title, access, children }: { title: string; access: Access; children: Snippet } = $props();

	let warning: string | undefined = $state();
	let timer: ReturnType<typeof setInterval> | undefined;

	async function readDisk() {
		const a = await admin<Retention>('GET', '/retention');
		if (a.access === 'open') warning = diskWarning(a.body?.disk);
	}

	$effect(() => {
		if (access !== 'open' || timer) return;
		void readDisk();
		timer = setInterval(() => void readDisk(), DISK_MS);
	});
	onDestroy(() => clearInterval(timer));

	const signIn = $derived(`/auth/signin?return=${encodeURIComponent(page.url.pathname + page.url.search)}`);
</script>

<svelte:head><title>{title} · Admin · RadioArchive</title></svelte:head>

{#if access === 'open'}
	<h1>{title}</h1>
	{#if warning}<p class="alert disk-warning">{warning}</p>{/if}
	{@render children()}
{:else if access === 'checking'}
	<h1>{title}</h1>
	<p class="meta" role="status">Loading…</p>
{:else if access === 'sign-in'}
	<h1>This page is for admins</h1>
	<p>Sign in as an admin to open it.</p>
	<p><a class="button" href={signIn}>Sign in</a></p>
{:else if access === 'not-admin'}
	<h1>This page is for admins</h1>
{:else}
	<h1>{title}</h1>
	<p class="alert" role="alert">Couldn't reach the server just now. Try again in a minute.</p>
{/if}

<style>
	/* An alert that stays (ArchiveUx section 4): the warning fill, no edge. */
	.alert {
		max-width: var(--measure);
		margin: 0 0 var(--space-lg);
		padding: var(--space-sm) var(--space-md);
		background: var(--warn-bg);
		color: var(--warn-fg);
		border-radius: var(--radius);
	}
	.meta {
		color: var(--muted);
		font-size: var(--text-sm);
	}
</style>
