<script lang="ts">
	// The Settings page (SPEC.md R88, R54, R53; ADR 0005, 0027, 0032, 0035),
	// an admin page: the retention period in days, the recordings volume's
	// state, how long the audit log keeps its entries (owner, 2026-11-25),
	// which asks first only when a shorter period deletes entries, naming
	// how many, and the setup export's download.
	//
	// Lengthening the period just saves. Shortening it is answered 409
	// confirm_shorter_retention with how many recordings the next nightly
	// run will delete; the page names that count and asks (ArchiveUx's
	// Confirm, inline), and only confirming saves it. Cancelling changes
	// nothing. The form follows ArchiveUx section 5: the Required tag, and
	// problems under the field.
	import { onMount, tick } from 'svelte';
	import { Confirm } from 'archiveux/components';
	import AdminPage from '#lib/admin/AdminPage.svelte';
	import { admin, deploymentZone, whenOf, type Access, type AdminError, type Retention } from '#lib/admin/api.ts';

	interface Shorter extends AdminError {
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

	// The audit log's own period (owner, 2026-11-25; ADR 0032): the same
	// form, asking first only when a shorter period deletes entries.
	interface AuditRetention {
		days: number;
		default_days: number;
		min_days: number;
		max_days: number;
		next_run: string;
		entries: number;
		oldest: string | null;
	}
	interface AuditShorter extends AdminError {
		days: number;
		entries: number;
	}
	let audit: AuditRetention | undefined = $state();
	let auditTyped = $state('');
	let auditProblem = $state('');
	let auditStatus = $state('');
	let auditSaving = $state(false);
	let auditAsking: { days: number; entries: number } | undefined = $state();

	async function loadAudit() {
		const a = await admin<AuditRetention>('GET', '/audit/retention');
		if (a.access !== 'open' || !a.body) return;
		audit = a.body;
		auditTyped = String(a.body.days);
	}

	onMount(() => {
		void deploymentZone().then((z) => (timeZone = z));
		void load();
		void loadAudit();
	});

	async function focusAudit() {
		await tick();
		document.getElementById('audit-days')?.focus();
	}

	async function sendAudit(days: number, confirm: boolean) {
		auditSaving = true;
		const a = await admin<AuditRetention | AuditShorter>('PUT', '/audit/retention', confirm ? { days, confirm } : { days });
		auditSaving = false;
		if (a.status === 409 && (a.body as AuditShorter | undefined)?.error === 'confirm_shorter_audit_retention') {
			const c = a.body as AuditShorter;
			auditAsking = { days: c.days, entries: c.entries };
			return;
		}
		if (a.access === 'sign-in' || a.access === 'not-admin') {
			access = a.access;
			return;
		}
		if (a.status === 400) {
			auditProblem = (a.body as AdminError | undefined)?.message ?? "That isn't a number of days.";
			await focusAudit();
			return;
		}
		if (a.access !== 'open' || !a.body) {
			auditStatus = "Couldn't save just now. Nothing changed. Try again in a minute.";
			return;
		}
		audit = a.body as AuditRetention;
		auditTyped = String(audit.days);
		auditAsking = undefined;
		auditStatus = `Saved: audit log entries are kept for ${plural(audit.days, 'day')}.`;
	}

	async function saveAudit(e: SubmitEvent) {
		e.preventDefault();
		if (!audit) return;
		auditStatus = '';
		auditAsking = undefined;
		const raw = auditTyped.trim();
		const days = Number(raw);
		if (raw === '') auditProblem = 'Enter how many days audit log entries are kept.';
		else if (!/^\d+$/.test(raw) || days < audit.min_days || days > audit.max_days)
			auditProblem = `Enter whole days from ${audit.min_days} to ${audit.max_days.toLocaleString('en-AU')}.`;
		else auditProblem = '';
		if (auditProblem) {
			await focusAudit();
			return;
		}
		if (days === audit.days) {
			auditStatus = `Nothing to change: audit log entries are kept for ${plural(days, 'day')}.`;
			return;
		}
		await sendAudit(days, false);
	}

	async function cancelAudit() {
		auditAsking = undefined;
		if (audit) {
			auditTyped = String(audit.days);
			auditStatus = `Nothing changed: audit log entries are still kept for ${plural(audit.days, 'day')}.`;
		}
		await focusAudit();
	}

	const plural = (n: number, one: string, many = `${one}s`) => `${n.toLocaleString('en-AU')} ${n === 1 ? one : many}`;

	async function focusField() {
		await tick();
		document.getElementById('days')?.focus();
	}

	async function send(days: number, confirm: boolean) {
		saving = true;
		const a = await admin<Retention | Shorter>('PUT', '/retention', confirm ? { days, confirm } : { days });
		saving = false;
		if (a.status === 409 && (a.body as Shorter | undefined)?.error === 'confirm_shorter_retention') {
			// Confirm takes the focus to its question.
			const c = a.body as Shorter;
			asking = { days: c.days, recordings: c.recordings };
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

	// The setup export's file (R53; ADR 0030, 0035): GET /admin/setup's
	// bytes as they are, saved as radioarchive-setup-<date>.json, the date
	// in the deployment's time zone.
	const exportName = $derived(`radioarchive-setup-${new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date())}.json`);

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
			<Confirm
				title="Delete {plural(asking.recordings, 'recording')}?"
				confirmLabel="Shorten to {plural(asking.days, 'day')}"
				level={3}
				busy={saving}
				onconfirm={() => send(asking!.days, true)}
				oncancel={cancel}
			>
				<p>
					Shortening retention from {plural(ret.days, 'day')} to {plural(asking.days, 'day')} deletes {plural(asking.recordings, 'recording')}, hidden talkgroups' included, at the next nightly run ({whenOf(ret.next_run, timeZone)}). They can't be brought back.
				</p>
			</Confirm>
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

		{#if audit}
			<h2>Audit log</h2>
			<form class="retention" novalidate onsubmit={saveAudit}>
				<div class="field">
					<label for="audit-days">Keep audit log entries (days) <span class="required">required</span></label>
					<input
						id="audit-days"
						type="text"
						inputmode="numeric"
						pattern="[0-9]*"
						autocomplete="off"
						required
						bind:value={auditTyped}
						aria-invalid={auditProblem ? 'true' : undefined}
						aria-describedby="audit-days-hint{auditProblem ? ' audit-days-error' : ''}"
					/>
					<p class="field-hint" id="audit-days-hint">
						From {audit.min_days} to {audit.max_days.toLocaleString('en-AU')} days; {audit.default_days} by default. Each night the entries older than this are deleted, and one entry says how many went.
						{#if audit.oldest}The log holds {plural(audit.entries, 'entry', 'entries')}, the oldest from {whenOf(audit.oldest, timeZone)}.{:else}The log is empty.{/if}
					</p>
					{#if auditProblem}<p class="field-error" id="audit-days-error">{auditProblem}</p>{/if}
				</div>
				<button type="submit" disabled={auditSaving} aria-label="Save the audit log's retention">Save</button>
			</form>

			{#if auditAsking}
				<Confirm
					title="Delete {plural(auditAsking.entries, 'audit log entry', 'audit log entries')}?"
					confirmLabel="Shorten to {plural(auditAsking.days, 'day')}"
					level={3}
					busy={auditSaving}
					onconfirm={() => sendAudit(auditAsking!.days, true)}
					oncancel={cancelAudit}
				>
					<p>
						Keeping audit log entries for {plural(auditAsking.days, 'day')} instead of {plural(audit.days, 'day')} deletes {plural(auditAsking.entries, 'entry', 'entries')} at the next nightly run ({whenOf(audit.next_run, timeZone)}). They can't be brought back.
					</p>
				</Confirm>
			{/if}
			<p class="audit-status" role="status">{auditStatus}</p>
		{/if}

		<h2>Setup export</h2>
		<p class="export">
			Every system with its talkgroups and units, and the groups and tags, with every field, as one JSON file: the file the PowerShell module's Import-RadioArchiveSetup reads, here or on another radio. Recorders, accounts, calls and the audit log aren't in it.
		</p>
		<p class="meta export" id="export-hint">It holds hidden and ignored talkgroups too: keep it as you would these pages.</p>
		<p><a class="button" href="/api/v1/admin/setup" download={exportName} aria-describedby="export-hint">Download the setup</a></p>
	{:else}
		<p class="meta" role="status">Loading…</p>
	{/if}
</AdminPage>

<style>
	.meta {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.retention,
	.export {
		max-width: var(--measure);
	}
	.retention input {
		width: var(--field-narrow);
	}
	.status,
	.audit-status {
		margin: var(--space-sm) 0 var(--space-xl);
	}
	.status:empty,
	.audit-status:empty {
		display: none;
	}
</style>
