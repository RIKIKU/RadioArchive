<script lang="ts">
	// The Settings page (SPEC.md R88; ADR 0005, 0027), an admin page: the
	// retention period in days, and the recordings volume's state.
	//
	// Lengthening the period just saves. Shortening it is answered 409
	// confirm_shorter_retention with how many recordings the next nightly
	// run will delete; the page names that count and asks, and only
	// confirming saves it. Cancelling changes nothing. The form follows
	// ArchiveUx section 5: the Required tag, and problems under the field.
	import { onMount, tick } from 'svelte';
	import AdminPage from '#lib/admin/AdminPage.svelte';
	import { admin, deploymentZone, whenOf, type Access, type AdminError, type Retention } from '#lib/admin/api.ts';

	interface Confirm extends AdminError {
		days: number;
		recordings: number;
	}

	let access: Access = $state('checking');
	let ret: Retention | undefined = $state();
	let timeZone = $state('UTC');
	let typed = $state('');
	let problem = $state('');
	let status = $state('');
	let saving = $state(false);
	/** A shorter period waiting for the admin's answer. */
	let asking: { days: number; recordings: number } | undefined = $state();

	async function load() {
		const a = await admin<Retention>('GET', '/retention');
		access = a.access;
		if (a.access !== 'open' || !a.body) return;
		ret = a.body;
		typed = String(a.body.days);
	}

	onMount(() => {
		void deploymentZone().then((z) => (timeZone = z));
		void load();
	});

	const plural = (n: number, one: string, many = `${one}s`) => `${n.toLocaleString('en-AU')} ${n === 1 ? one : many}`;

	async function focusField() {
		await tick();
		document.getElementById('days')?.focus();
	}

	async function send(days: number, confirm: boolean) {
		saving = true;
		const a = await admin<Retention | Confirm>('PUT', '/retention', confirm ? { days, confirm } : { days });
		saving = false;
		if (a.status === 409 && (a.body as Confirm | undefined)?.error === 'confirm_shorter_retention') {
			const c = a.body as Confirm;
			asking = { days: c.days, recordings: c.recordings };
			await tick();
			document.getElementById('confirm-heading')?.focus();
			return;
		}
		if (a.access === 'sign-in' || a.access === 'not-admin') {
			access = a.access;
			return;
		}
		if (a.status === 400) {
			problem = (a.body as AdminError | undefined)?.message ?? "That isn't a number of days.";
			await focusField();
			return;
		}
		if (a.access !== 'open' || !a.body) {
			status = "Couldn't save just now. Nothing changed. Try again in a minute.";
			return;
		}
		ret = a.body as Retention;
		typed = String(ret.days);
		asking = undefined;
		status = `Saved: recordings are kept for ${plural(ret.days, 'day')}.`;
	}

	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!ret) return;
		status = '';
		asking = undefined;
		const raw = typed.trim();
		const days = Number(raw);
		if (raw === '') problem = 'Enter how many days recordings are kept.';
		else if (!/^\d+$/.test(raw) || days < ret.min_days || days > ret.max_days) problem = `Enter whole days from ${ret.min_days} to ${ret.max_days.toLocaleString('en-AU')}.`;
		else problem = '';
		if (problem) {
			await focusField();
			return;
		}
		if (days === ret.days) {
			status = `Nothing to change: recordings are kept for ${plural(days, 'day')}.`;
			return;
		}
		await send(days, false);
	}

	async function cancel() {
		asking = undefined;
		if (ret) {
			typed = String(ret.days);
			status = `Nothing changed: recordings are still kept for ${plural(ret.days, 'day')}.`;
		}
		await focusField();
	}

	const gb = (bytes: number) => `${(bytes / 1e9).toLocaleString('en-AU', { maximumFractionDigits: 1 })} GB`;
</script>

<AdminPage title="Settings" {access}>
	{#if ret}
		<h2>Retention</h2>
		<form class="retention" novalidate onsubmit={save}>
			<div class="field">
				<label for="days">Retention period (days) <span class="required">required</span></label>
				<input
					id="days"
					type="text"
					inputmode="numeric"
					pattern="[0-9]*"
					autocomplete="off"
					required
					bind:value={typed}
					aria-invalid={problem ? 'true' : undefined}
					aria-describedby="days-hint{problem ? ' days-error' : ''}"
				/>
				<p class="field-hint" id="days-hint">
					From {ret.min_days} to {ret.max_days.toLocaleString('en-AU')} days; {ret.default_days} by default. Each night the oldest recordings past it are deleted; the next run is {whenOf(ret.next_run, timeZone)}.
				</p>
				{#if problem}<p class="field-error" id="days-error">{problem}</p>{/if}
			</div>
			<button type="submit" disabled={saving}>Save</button>
		</form>

		{#if asking}
			<section class="confirm" aria-labelledby="confirm-heading">
				<h3 id="confirm-heading" tabindex="-1">Delete {plural(asking.recordings, 'recording')}?</h3>
				<p>
					Shortening retention from {plural(ret.days, 'day')} to {plural(asking.days, 'day')} deletes {plural(asking.recordings, 'recording')}, hidden talkgroups' included, at the next nightly run ({whenOf(ret.next_run, timeZone)}). They can't be brought back.
				</p>
				<div class="actions">
					<button type="button" class="danger" disabled={saving} onclick={() => send(asking!.days, true)}>Shorten to {plural(asking.days, 'day')}</button>
					<button type="button" class="secondary" onclick={cancel}>Cancel</button>
				</div>
			</section>
		{/if}
		<p class="status" role="status">{status}</p>

		<h2>Recordings volume</h2>
		{#if ret.disk}
			<p>{ret.disk.free_percent} % free: {gb(ret.disk.free_bytes)} of {gb(ret.disk.total_bytes)}.</p>
			<p class="meta">
				Admins are warned below {ret.disk.warning_free_percent} % free. Below {ret.disk.min_free_percent} %, the oldest recordings are deleted early, but never one from the last day.
			</p>
		{:else}
			<p class="meta">The recordings volume couldn't be measured just now.</p>
		{/if}
	{:else}
		<p class="meta" role="status">Loading…</p>
	{/if}
</AdminPage>

<style>
	.meta {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.retention {
		max-width: var(--measure);
	}
	.retention input {
		width: var(--field-narrow);
	}
	.confirm {
		max-width: var(--panel-max);
		margin: var(--space-md) 0;
		padding: var(--space-lg);
		background: var(--surface);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
	}
	.confirm h3 {
		margin-top: 0;
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-sm);
	}
	.status {
		margin: var(--space-sm) 0 var(--space-xl);
	}
	.status:empty {
		display: none;
	}
</style>
