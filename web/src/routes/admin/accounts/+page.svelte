<script lang="ts">
	// The Accounts page (SPEC.md R55, R84, R111; ADR 0025, 0033), an admin
	// page: everyone who has signed in to the radio, with their name and
	// email, role, status and first and last sign-in, searched in the
	// browser. An admin makes others admins or listeners, disables and
	// enables them, and deletes them; never their own account, and never
	// the last active admin (the API checks both, with the change).
	//
	// - Disabling signs them out of the radio at once and refuses their next
	//   sign-in, then switches their Authentik user off. If Authentik
	//   doesn't take it, the radio's change stands and the page says so,
	//   with the account marked "not yet in Authentik"; doing it again tries
	//   Authentik again. Enabling mirrors it, and their saved settings are as
	//   they left them.
	// - Deleting removes the account, its saved settings and its sessions,
	//   and its Authentik user; the question (ArchiveUx's Confirm, a dialog
	//   on phones) names what goes. If Authentik doesn't take it, nothing is
	//   deleted.
	//
	// ArchiveUx 0.5.0's DataTable (a card each on phones), StateBadge,
	// MoreActions and Confirm.
	import { onMount } from 'svelte';
	import { Banner, Confirm, DataTable, MoreActions, StateBadge } from 'archiveux/components';
	import AdminPage from '#lib/admin/AdminPage.svelte';
	import { admin, deploymentZone, whenOf, type Access, type AdminError } from '#lib/admin/api.ts';
	import { accountLabel, matches, statusOf, type Account } from '#lib/admin/accounts.ts';

	interface Me {
		id: string;
	}
	interface Missed extends AdminError {
		account?: Account;
	}
	/** What the admin is being asked to confirm. */
	type Asking = { kind: 'disable' | 'admin' | 'listener' | 'delete'; account: Account };

	let access: Access = $state('checking');
	let accounts: Account[] = $state([]);
	let me = $state('');
	let timeZone = $state('UTC');
	let query = $state('');
	let asking: Asking | undefined = $state();
	let busy = $state(false);
	/** The last change's result, in words (polite). */
	let said = $state('');
	/** Authentik's part of the last change, when it didn't take (an alert). */
	let missed = $state('');
	let sort: { key: string; dir: 'ascending' | 'descending' } | null = $state(null);
	let phone = $state(false);

	async function load() {
		const [a, m] = await Promise.all([admin<Account[]>('GET', '/accounts'), admin<Me>('GET', '/me')]);
		access = a.access;
		if (a.access !== 'open' || !a.body) return;
		accounts = a.body;
		if (m.access === 'open' && m.body) me = m.body.id;
	}

	onMount(() => {
		void deploymentZone().then((z) => (timeZone = z));
		void load();
		const narrow = window.matchMedia('(max-width: 40rem)');
		phone = narrow.matches;
		const changed = () => (phone = narrow.matches);
		narrow.addEventListener('change', changed);
		return () => narrow.removeEventListener('change', changed);
	});

	const shown = $derived(accounts.filter((a) => matches(a, query)));

	const columns = [
		{ key: 'name', label: 'Name', sortable: true, primary: true, value: (a: Account) => accountLabel(a) },
		{ key: 'email', label: 'Email', sortable: true },
		{ key: 'role', label: 'Role', sortable: true, text: (a: Account) => (a.role === 'admin' ? 'Admin' : 'Listener') },
		{ key: 'status', label: 'Status', custom: true, sortable: true, value: (a: Account) => statusOf(a).words },
		{ key: 'first_signed_in', label: 'First signed in', sortable: true, firstDir: 'descending' as const, text: (a: Account) => whenOf(a.first_signed_in, timeZone) },
		{ key: 'last_signed_in', label: 'Last signed in', sortable: true, firstDir: 'descending' as const, text: (a: Account) => whenOf(a.last_signed_in, timeZone) }
	];

	const plural = (n: number, one: string, many = `${one}s`) => `${n.toLocaleString('en-AU')} ${n === 1 ? one : many}`;

	/** Sends one change, then shows what happened and reads the list again. */
	async function send(method: string, a: Account, body: unknown, done: string) {
		busy = true;
		said = '';
		missed = '';
		const r = await admin<Account | Missed>(method, `/accounts/${encodeURIComponent(a.id)}`, body);
		busy = false;
		asking = undefined;
		if (r.access === 'sign-in' || r.access === 'not-admin') {
			access = r.access;
			return;
		}
		const err = r.body as Missed | undefined;
		if (err?.error?.startsWith('authentik_')) {
			// The radio's change stands (or, for a deletion, nothing changed).
			missed = err.message;
		} else if (r.status >= 200 && r.status < 300) {
			said = done;
		} else if (r.status >= 400 && r.status < 500 && err?.message) {
			missed = err.message;
		} else {
			missed = `Couldn't reach the server just now. Nothing changed for ${accountLabel(a)}. Try again in a minute.`;
		}
		await load();
	}

	function confirm() {
		if (!asking) return;
		const a = asking.account;
		const name = accountLabel(a);
		switch (asking.kind) {
			case 'disable':
				return send('PATCH', a, { status: 'disabled' }, `Disabled ${name}: they're signed out and can't sign in until they're enabled.`);
			case 'admin':
				return send('PATCH', a, { role: 'admin' }, `${name} is an admin now.`);
			case 'listener':
				return send('PATCH', a, { role: 'listener' }, `${name} is a listener now.`);
			case 'delete':
				return send('DELETE', a, undefined, `Deleted ${name}'s account.`);
		}
	}

	function cancel() {
		if (asking) said = `Nothing changed for ${accountLabel(asking.account)}.`;
		asking = undefined;
	}

	const enable = (a: Account) => send('PATCH', a, { status: 'active' }, `Enabled ${accountLabel(a)}: they can sign in again.`);
	const retry = (a: Account) => send('PATCH', a, { status: a.status }, `Authentik has it now: ${accountLabel(a)} is ${a.status === 'disabled' ? 'disabled' : 'enabled'} there too.`);

	const titleOf = (k: Asking) => {
		const name = accountLabel(k.account);
		return { disable: `Disable ${name}?`, admin: `Make ${name} an admin?`, listener: `Make ${name} a listener?`, delete: `Delete ${name}'s account?` }[k.kind];
	};
	const labelOf = (k: Asking) => {
		const name = accountLabel(k.account);
		return { disable: `Disable ${name}`, admin: `Make ${name} an admin`, listener: `Make ${name} a listener`, delete: `Delete ${name}'s account` }[k.kind];
	};
</script>

<AdminPage title="Accounts" {access}>
	<p class="intro">Everyone who has signed in to the radio. Admins change other accounts here; nobody changes their own.</p>
	<div class="field search">
		<label for="accounts-search">Search by name or email</label>
		<input id="accounts-search" type="search" autocomplete="off" bind:value={query} aria-describedby="accounts-count" />
		<p class="field-hint" id="accounts-count" aria-live="polite">
			{query.trim() ? `${plural(shown.length, 'account')} of ${accounts.length.toLocaleString('en-AU')} match.` : plural(accounts.length, 'account')}
		</p>
	</div>

	{#if missed}
		<div class="missed">
			<Banner tone="error" role="alert"><p>{missed}</p></Banner>
		</div>
	{/if}
	<p class="status" role="status">{said}</p>

	<!-- Room under the table, so the last row's More actions opens clear of the dock. -->
	<div class="table-room">
		<DataTable {columns} rows={shown} caption="Accounts" bind:sort key={(a: Account) => a.id} empty={query.trim() ? 'No account matches.' : 'Nobody has signed in yet.'}>
			{#snippet cell(a: Account)}
				{@const s = statusOf(a)}
				<StateBadge state={s.state} text={s.words} />
			{/snippet}
			{#snippet actions(a: Account)}
				{@const name = accountLabel(a)}
				{#if a.id === me}
					<span class="you">You</span>
				{:else}
					{#if a.status === 'disabled'}
						<button type="button" class="secondary" disabled={busy} aria-label="Enable {name}" onclick={() => enable(a)}>Enable</button>
					{:else}
						<button type="button" class="secondary" disabled={busy} aria-label="Disable {name}" onclick={() => (asking = { kind: 'disable', account: a })}>Disable…</button>
					{/if}
					<!-- Its words, not a tooltip: the last column's tooltip would reach past the page's edge. -->
					<MoreActions label="More actions: {name}" text="More" align={phone ? 'start' : 'end'}>
						{#if a.authentik_pending}
							<button type="button" onclick={() => retry(a)}>Try Authentik again</button>
						{/if}
						{#if a.role === 'admin'}
							<button type="button" onclick={() => (asking = { kind: 'listener', account: a })}>Make a listener…</button>
						{:else}
							<button type="button" onclick={() => (asking = { kind: 'admin', account: a })}>Make an admin…</button>
						{/if}
						<hr />
						<button type="button" class="danger" onclick={() => (asking = { kind: 'delete', account: a })}>Delete account…</button>
					</MoreActions>
				{/if}
			{/snippet}
		</DataTable>
	</div>

	{#if asking}
		{@const a = asking.account}
		{@const name = accountLabel(a)}
		<Confirm
			title={titleOf(asking)}
			confirmLabel={labelOf(asking)}
			danger={asking.kind === 'disable' || asking.kind === 'delete'}
			modal={phone}
			level={2}
			{busy}
			onconfirm={confirm}
			oncancel={cancel}
		>
			{#if asking.kind === 'disable'}
				<p>
					{name}{a.email && a.email !== name ? ` (${a.email})` : ''} is signed out of the radio at once, everywhere, and can't sign in until an admin enables them. Their user is switched off in Authentik too. Their saved settings are kept. Listening without signing in is the same for them as for anyone.
				</p>
			{:else if asking.kind === 'admin'}
				<p>{name} can then change everything on the admin pages, other admins' accounts included.</p>
			{:else if asking.kind === 'listener'}
				<p>{name} can't open the admin pages from their next request.</p>
			{:else}
				<p>
					This deletes {name}{a.email && a.email !== name ? ` (${a.email})` : ''} from the radio: their account, their saved settings (listening set, look and download basket) and every session, and their user in Authentik, so they can't sign in. It can't be undone. The audit log keeps who they were.
				</p>
			{/if}
		</Confirm>
	{/if}
</AdminPage>

<style>
	.intro {
		max-width: var(--measure);
	}
	.search {
		max-width: var(--measure);
	}
	.status {
		margin: var(--space-sm) 0;
	}
	.status:empty {
		display: none;
	}
	.missed {
		margin: var(--space-sm) 0;
	}
	/* A More actions list is up to four rows high, under its button. */
	.table-room {
		padding-block-end: calc(var(--target) * 4);
	}
	.you {
		color: var(--muted);
		font-size: var(--text-sm);
	}
</style>
