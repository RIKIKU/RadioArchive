<script lang="ts">
	// The frame of every admin page (SPEC.md R84, R88, R89; ADR 0027, 0041),
	// inside ArchiveUx's frame: the page's heading, the deployment's alerts,
	// and the page itself once the admin API has answered it as an admin's.
	//
	// The admin API is the guard (ADR 0025): access comes from the answer to
	// the page's own data. Signed out, the page offers sign-in; signed in
	// but not an admin, it says "This page is for admins" and nothing else.
	//
	// The deployment's alerts stay until what they say is fixed (ArchiveUx
	// section 4, "Alerts": a Banner), on every admin page and on no
	// listener page:
	// - the disk warning (R88: "a warning shows to admins at 15 % free"),
	//   GET /admin/retention's volume state;
	// - the backup failing (R89: "backup state shows to admins as ArchiveUx
	//   alerts"), GET /admin/backup's state, when the last backup failed or
	//   the last good one is more than 36 hours old;
	// - an automatic upgrade failing (R87, R89; ADR 0044), GET /admin/upgrade's
	//   state, when an upgrade failed or a run stopped part way, until a later
	//   upgrade or the script's clear.
	import type { Snippet } from 'svelte';
	import { Banner } from 'archiveux/components';
	import { onDestroy } from 'svelte';
	import { page } from '$app/state';
	import { admin, backupAlert, diskWarning, upgradeAlert, type Access, type Backup, type Retention, type Upgrade } from './api.ts';

	/** How often the deployment's state is asked again. */
	const STATE_MS = 60_000;

	let { title, access, children }: { title: string; access: Access; children: Snippet } = $props();

	let warning: string | undefined = $state();
	let backup: string | undefined = $state();
	let upgrade: string | undefined = $state();
	let timer: ReturnType<typeof setInterval> | undefined;

	async function readState() {
		const [r, b, u] = await Promise.all([
			admin<Retention>('GET', '/retention'),
			admin<Backup>('GET', '/backup'),
			admin<Upgrade>('GET', '/upgrade')
		]);
		if (r.access === 'open') warning = diskWarning(r.body?.disk);
		if (b.access === 'open') backup = backupAlert(b.body);
		if (u.access === 'open') upgrade = upgradeAlert(u.body);
	}

	$effect(() => {
		if (access !== 'open' || timer) return;
		void readState();
		timer = setInterval(() => void readState(), STATE_MS);
	});
	onDestroy(() => clearInterval(timer));

	const signIn = $derived(`/auth/signin?return=${encodeURIComponent(page.url.pathname + page.url.search)}`);
</script>

<svelte:head><title>{title} · Admin · RadioArchive</title></svelte:head>

{#if access === 'open'}
	<h1>{title}</h1>
	{#if backup}<Banner tone="error"><p><strong>Backups are failing.</strong> {backup} See the deploy guide's "Backups".</p></Banner>{/if}
	{#if upgrade}<Banner tone="error"><p><strong>The automatic upgrade failed.</strong> {upgrade} See the deploy guide's "Automatic upgrades".</p></Banner>{/if}
	{#if warning}<Banner><p>{warning}</p></Banner>{/if}
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
	<Banner tone="error" role="alert"><p>Couldn't reach the server just now. Try again in a minute.</p></Banner>
{/if}

<style>
	.meta {
		color: var(--muted);
		font-size: var(--text-sm);
	}
</style>
