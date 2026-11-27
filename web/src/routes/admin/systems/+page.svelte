<script lang="ts">
	// The Systems page (SPEC.md R11, R12, R15, R21, R22, R80, R83; A2, A36;
	// ADR 0003, 0006, 0031, 0034), an admin page in four sections:
	//
	// - Systems: list, add (ID, label, name), edit, and auto-create, off by
	//   default (switching it on asks first); each one's alert, LED colour
	//   and sort order. Deleting one asks first, and a system that still has
	//   talkgroups or calls is refused with the server's own words
	//   (409 system_in_use).
	// - Attachments (R12): each recorder + shortName and its system, moved
	//   from the row's More actions or removed after asking. A change
	//   affects only future calls (ADR 0003), and the page says so.
	// - Not attached: the pairs whose calls were refused, in R12's words,
	//   each attached from there.
	// - Tags and groups (R22, ADR 0006): their names, alert, LED colour and
	//   sort order, which their talkgroups with none of their own take.
	//
	// One system, tag or group is open for editing at a time, in a panel
	// above its table (SettingsEditor), as on the Talkgroups page.
	import { onMount, tick } from 'svelte';
	import { Banner, Confirm, DataTable, MoreActions, StateBadge } from 'archiveux/components';
	import AdminPage from '#lib/admin/AdminPage.svelte';
	import SettingsEditor from '#lib/admin/SettingsEditor.svelte';
	import { admin, deploymentZone, whenOf, type Access, type AdminError } from '#lib/admin/api.ts';
	import type { Label } from '#lib/admin/talkgroups.ts';
	import { pairOf, settingWords, unattached, type Attachment, type FullSystem, type Kind, type Unattached } from '#lib/admin/systems.ts';

	type Column<T> = { key: string; label: string; sortable?: boolean; firstDir?: 'ascending' | 'descending'; align?: 'start' | 'end'; primary?: boolean; custom?: boolean; value?: (r: T) => unknown; text?: (r: T) => string };
	type Sort = { key: string; dir: 'ascending' | 'descending' };

	let access: Access = $state('checking');
	let systems: FullSystem[] = $state([]);
	let attachments: Attachment[] = $state([]);
	let rejected: Unattached[] = $state([]);
	let tags: Label[] = $state([]);
	let groups: Label[] = $state([]);
	let loaded = $state(false);
	let timeZone = $state('UTC');

	let status = $state('');
	let problem = $state('');
	/** The one system, tag or group open for editing; id 0 is a new one. */
	let open: { kind: Kind; id: number } | undefined = $state();
	let opener: (() => HTMLElement | null) | undefined;

	/** Something destructive waiting for the admin's answer. */
	let asking: { what: 'system'; system: FullSystem } | { what: 'detach'; att: Attachment } | undefined = $state();
	let askError = $state('');
	/** Each unattached pair's chosen system, by pair. */
	let attachTo: Record<string, string> = $state({});
	let busy = $state(false);

	let sysSort: Sort | null = $state(null);
	let attSort: Sort | null = $state(null);
	let tagSort: Sort | null = $state(null);
	let groupSort: Sort | null = $state(null);

	async function loadPairs() {
		const [a, j] = await Promise.all([admin<Attachment[]>('GET', '/attachments'), admin<{ unattached: Unattached[] }>('GET', '/rejections')]);
		if (a.access === 'open' && a.body) attachments = a.body;
		if (j.access === 'open' && j.body) rejected = j.body.unattached;
	}

	async function load() {
		const [s, t, g] = await Promise.all([admin<FullSystem[]>('GET', '/systems'), admin<Label[]>('GET', '/tags'), admin<Label[]>('GET', '/groups'), loadPairs()]);
		access = s.access;
		if (s.access !== 'open' || !s.body) return;
		systems = s.body;
		if (t.access === 'open' && t.body) tags = t.body;
		if (g.access === 'open' && g.body) groups = g.body;
		loaded = true;
	}

	onMount(() => {
		void deploymentZone().then((z) => (timeZone = z));
		void load();
	});

	const notAttached = $derived(unattached(rejected, attachments));
	const plural = (x: number, one: string, many = `${one}s`) => `${x.toLocaleString('en-AU')} ${x === 1 ? one : many}`;
	const pairWords = (p: { recorder: string; short_name: string }) => `${p.recorder} / ${p.short_name}`;

	const settingColumns = <T extends { alert: string | null; led_colour: string | null; sort_order: number }>(): Column<T>[] => [
		{ key: 'alert', label: 'Alert', sortable: true, value: (r) => r.alert, text: (r) => settingWords(r.alert) || 'Not set' },
		{ key: 'led_colour', label: 'LED colour', sortable: true, custom: true, value: (r) => r.led_colour },
		{ key: 'sort_order', label: 'Sort order', sortable: true, align: 'end' }
	];
	const systemColumns: Column<FullSystem>[] = [
		{ key: 'id', label: 'ID', sortable: true, primary: true, align: 'end' },
		{ key: 'label', label: 'Label', sortable: true },
		{ key: 'name', label: 'Name', sortable: true },
		{ key: 'auto_create', label: 'Auto-create', sortable: true, custom: true, value: (s) => (s.auto_create ? 0 : 1) },
		...settingColumns<FullSystem>()
	];
	const attachmentColumns: Column<Attachment>[] = [
		{ key: 'pair', label: 'Recorder / shortName', sortable: true, primary: true, value: (a) => pairWords(a), text: (a) => pairWords(a) },
		{ key: 'system_label', label: 'System', sortable: true },
		{ key: 'attached_at', label: 'Attached', sortable: true, firstDir: 'descending', value: (a) => (a.attached_at ? Date.parse(a.attached_at) : null), text: (a) => (a.attached_at ? whenOf(a.attached_at, timeZone) : '') }
	];
	const labelColumns: Column<Label>[] = [{ key: 'name', label: 'Name', sortable: true, primary: true }, ...settingColumns<Label>()];
	// A tag's always show (R115; ADR 0054): its talkgroups are never quiet.
	const tagColumns: Column<Label>[] = [
		...labelColumns,
		{ key: 'always_show', label: 'Always show', sortable: true, value: (t) => (t.always_show ? 0 : 1), text: (t) => (t.always_show ? 'Yes' : 'No') }
	];

	function show(kind: Kind, id: number, back: () => HTMLElement | null) {
		status = '';
		problem = '';
		opener = back;
		open = { kind, id };
	}

	async function close(said = '') {
		open = undefined;
		if (said) status = said;
		await tick();
		const el = opener?.();
		opener = undefined;
		if (el && el.isConnected) el.focus();
	}

	const thingOf = (kind: Kind, id: number): FullSystem | Label | undefined =>
		id === 0 ? undefined : kind === 'system' ? systems.find((s) => s.id === id) : (kind === 'tag' ? tags : groups).find((x) => x.id === id);

	async function saved(kind: Kind, t: FullSystem | Label, said: string) {
		const put = <T extends { id: number }>(list: T[], x: T) => (list.some((y) => y.id === x.id) ? list.map((y) => (y.id === x.id ? x : y)) : [...list, x]);
		if (kind === 'system') {
			const isNew = !systems.some((s) => s.id === t.id);
			systems = put(systems, t as FullSystem);
			// A new label shows on its attachments, and a new system is a choice for them.
			if (!isNew) attachments = attachments.map((a) => (a.system_id === t.id ? { ...a, system_label: (t as FullSystem).label } : a));
			if (isNew) opener = () => document.querySelector<HTMLElement>(`button[aria-label="${CSS.escape(`Edit system ${(t as FullSystem).label}`)}"]`);
		} else if (kind === 'tag') tags = put(tags, t as Label);
		else {
			const isNew = !groups.some((g) => g.id === t.id);
			groups = put(groups, t as Label);
			if (isNew) opener = () => document.querySelector<HTMLElement>(`button[aria-label="${CSS.escape(`Edit group ${t.name}`)}"]`);
		}
		await close(said);
	}

	function denied(a: { access: Access }): boolean {
		if (a.access === 'sign-in' || a.access === 'not-admin') {
			access = a.access;
			return true;
		}
		return false;
	}

	async function deleteSystem(s: FullSystem) {
		askError = '';
		const a = await admin<AdminError>('DELETE', `/systems/${s.id}`);
		if (denied(a)) return;
		if (a.status === 204) {
			systems = systems.filter((x) => x.id !== s.id);
			asking = undefined;
			status = `Deleted system ${s.label}.`;
			await loadPairs();
			return;
		}
		// 409 system_in_use: the server's own words, in the dialog.
		askError = a.body?.message ?? "Couldn't delete it just now. Nothing changed. Try again in a minute.";
	}

	async function attach(p: { recorder: string; short_name: string }, systemID: number, verb: 'Attached' | 'Moved') {
		status = '';
		problem = '';
		busy = true;
		const a = await admin<Attachment | AdminError>('PUT', `/recorders/${encodeURIComponent(p.recorder)}/attachments/${encodeURIComponent(p.short_name)}`, { system_id: systemID });
		busy = false;
		if (denied(a)) return false;
		if (a.access !== 'open' || !a.body || 'error' in a.body) {
			problem = a.body && 'error' in a.body ? a.body.message : `Couldn't change ${pairWords(p)} just now. Nothing changed. Try again in a minute.`;
			return false;
		}
		const att = a.body;
		attachments = [...attachments.filter((x) => pairOf(x) !== pairOf(att)), att].sort((x, y) => pairOf(x).localeCompare(pairOf(y)));
		status = `${verb} ${pairWords(att)} to ${att.system_label}. Its calls from now on are filed under ${att.system_label}; calls already kept or refused stay as they were.`;
		return true;
	}

	async function attachPair(e: SubmitEvent, p: Unattached) {
		e.preventDefault();
		const sys = Number(attachTo[pairOf(p)] ?? systems[0]?.id);
		if (await attach(p, sys, 'Attached')) {
			await tick();
			document.getElementById('attachments-heading')?.focus();
		}
	}

	async function detach(att: Attachment) {
		askError = '';
		const a = await admin<AdminError>('DELETE', `/recorders/${encodeURIComponent(att.recorder)}/attachments/${encodeURIComponent(att.short_name)}`);
		if (denied(a)) return;
		if (a.status === 204 || a.status === 404) {
			attachments = attachments.filter((x) => pairOf(x) !== pairOf(att));
			asking = undefined;
			status = `Removed ${pairWords(att)}'s attachment. Its calls from now on are refused and counted under Not attached.`;
			await tick();
			document.getElementById('attachments-heading')?.focus();
			return;
		}
		askError = a.body?.message ?? "Couldn't remove it just now. Nothing changed. Try again in a minute.";
	}
</script>

{#snippet led(v: string | null)}
	{#if v === null}<span class="none">Not set</span>{:else if v === 'none'}None{:else}<span class="led" data-led={v} aria-hidden="true"></span> {v}{/if}
{/snippet}

{#snippet editor(kind: Kind)}
	{#if open?.kind === kind}
		{#key `${open.kind}/${open.id}`}
			<SettingsEditor
				{kind}
				thing={thingOf(kind, open.id)}
				onsaved={(t, said) => saved(kind, t, said)}
				oncancel={() => close(open && open.id ? 'Nothing changed.' : '')}
				onaccess={(a) => (access = a)}
			/>
		{/key}
	{/if}
{/snippet}

<AdminPage title="Systems" {access}>
	<nav class="contents" aria-label="On this page">
		<a href="#systems-heading">Systems</a>
		<a href="#attachments-heading">Attachments</a>
		<a href="#unattached-heading">Not attached</a>
		<a href="#tags-heading">Tags</a>
		<a href="#groups-heading">Groups</a>
	</nav>

	<p class="status" role="status">{status}</p>
	{#if problem}<Banner tone="error" role="alert"><p>{problem}</p></Banner>{/if}

	<section aria-labelledby="systems-heading">
		<div class="heading-row">
			<h2 id="systems-heading" tabindex="-1">Systems</h2>
			<button
				type="button"
				class="secondary"
				onclick={(e) => {
					const b = e.currentTarget;
					show('system', 0, () => b);
				}}>Add a system</button
			>
		</div>
		{@render editor('system')}
		<div class="table-edge">
		<DataTable
			caption="Systems"
			columns={systemColumns}
			rows={systems}
			key={(s: FullSystem) => s.id}
			bind:sort={sysSort}
			empty={loaded ? 'No systems yet. Add one, or bring them in with the import.' : 'Loading…'}
			rowProps={(s: FullSystem) => ({ 'data-open': open?.kind === 'system' && open.id === s.id ? 'edit' : undefined })}
		>
			{#snippet cell(s: FullSystem, c: Column<FullSystem>)}
				{#if c.key === 'auto_create'}
					<StateBadge state={s.auto_create ? 'warning' : 'neutral'} text={s.auto_create ? 'On' : 'Off'} />
				{:else if c.key === 'led_colour'}{@render led(s.led_colour)}{/if}
			{/snippet}
			{#snippet actions(s: FullSystem)}
				<button
					type="button"
					class="secondary"
					aria-label="Edit system {s.label}"
					onclick={(e) => {
						const b = e.currentTarget;
						show('system', s.id, () => b);
					}}>Edit</button
				>
				<MoreActions label="More actions: system {s.label}">
					<button
						type="button"
						class="danger"
						onclick={() => {
							askError = '';
							asking = { what: 'system', system: s };
						}}>Delete…</button
					>
				</MoreActions>
			{/snippet}
		</DataTable>
		</div>
	</section>

	<section aria-labelledby="attachments-heading">
		<h2 id="attachments-heading" tabindex="-1">Attachments</h2>
		<p class="intro">Each recorder's system (its shortName) is attached to one system, which its calls are filed under. A change affects only future calls: calls already kept stay under the system they were filed under, and calls already refused stay refused.</p>
		<div class="table-edge">
		<DataTable
			caption="Attachments"
			columns={attachmentColumns}
			rows={attachments}
			key={pairOf}
			bind:sort={attSort}
			empty={loaded ? 'Nothing is attached yet. A recorder\'s calls are refused until its system is attached.' : 'Loading…'}
		>
			{#snippet actions(a: Attachment)}
				<MoreActions label="More actions: {pairWords(a)}">
					{#each systems.filter((s) => s.id !== a.system_id) as s (s.id)}
						<button type="button" disabled={busy} onclick={() => attach(a, s.id, 'Moved')}>Move to {s.label}</button>
					{/each}
					{#if systems.length > 1}<hr />{/if}
					<button
						type="button"
						class="danger"
						onclick={() => {
							askError = '';
							asking = { what: 'detach', att: a };
						}}>Remove…</button
					>
				</MoreActions>
			{/snippet}
		</DataTable>
		</div>
	</section>

	<section aria-labelledby="unattached-heading">
		<h2 id="unattached-heading">Not attached</h2>
		{#if notAttached.length && systems.length}
			<p class="intro">These recorders' systems sent calls that were refused, because they aren't attached. Attach one to keep its calls from then on; the calls already refused aren't kept.</p>
			<ul class="pairs" aria-labelledby="unattached-heading">
				{#each notAttached as p (pairOf(p))}
					<li>
						<p>{p.message}</p>
						<form class="attach" novalidate onsubmit={(e) => attachPair(e, p)}>
							<label for="attach-{pairOf(p)}">System for {pairWords(p)}</label>
							<select id="attach-{pairOf(p)}" value={attachTo[pairOf(p)] ?? String(systems[0].id)} onchange={(e) => (attachTo[pairOf(p)] = e.currentTarget.value)}>
								{#each systems as s (s.id)}<option value={String(s.id)}>{s.label}</option>{/each}
							</select>
							<button type="submit" disabled={busy} aria-label="Attach {pairWords(p)}">Attach</button>
						</form>
					</li>
				{/each}
			</ul>
		{:else if notAttached.length}
			<ul class="pairs" aria-labelledby="unattached-heading">
				{#each notAttached as p (pairOf(p))}<li><p>{p.message}</p></li>{/each}
			</ul>
			<p class="meta">Add a system above to attach them to.</p>
		{:else}
			<p class="meta">No calls have been refused for coming from a system that isn't attached.</p>
		{/if}
	</section>

	<section aria-labelledby="tags-heading">
		<h2 id="tags-heading">Tags</h2>
		<p class="intro">Every talkgroup has one tag, set on the Talkgroups page, where a new tag is made when it's first used. A talkgroup with no alert or LED colour of its own takes its tag's, then its groups', then its system's. A tag's talkgroups shown always are never quiet.</p>
		{@render editor('tag')}
		<div class="table-edge">
		<DataTable
			caption="Tags"
			columns={tagColumns}
			rows={tags}
			key={(t: Label) => t.id}
			bind:sort={tagSort}
			empty={loaded ? 'No tags yet.' : 'Loading…'}
			rowProps={(t: Label) => ({ 'data-open': open?.kind === 'tag' && open.id === t.id ? 'edit' : undefined })}
		>
			{#snippet cell(t: Label, c: Column<Label>)}
				{#if c.key === 'led_colour'}{@render led(t.led_colour)}{/if}
			{/snippet}
			{#snippet actions(t: Label)}
				<button
					type="button"
					class="secondary"
					aria-label="Edit tag {t.name}"
					onclick={(e) => {
						const b = e.currentTarget;
						show('tag', t.id, () => b);
					}}>Edit</button
				>
			{/snippet}
		</DataTable>
		</div>
	</section>

	<section aria-labelledby="groups-heading">
		<div class="heading-row">
			<h2 id="groups-heading">Groups</h2>
			<button
				type="button"
				class="secondary"
				onclick={(e) => {
					const b = e.currentTarget;
					show('group', 0, () => b);
				}}>Add a group</button
			>
		</div>
		<p class="intro">A talkgroup is in any number of groups, set on the Talkgroups page. Of a talkgroup's groups, the first by sort order that sets an alert or LED colour gives it.</p>
		{@render editor('group')}
		<div class="table-edge">
		<DataTable
			caption="Groups"
			columns={labelColumns}
			rows={groups}
			key={(g: Label) => g.id}
			bind:sort={groupSort}
			empty={loaded ? 'No groups yet.' : 'Loading…'}
			rowProps={(g: Label) => ({ 'data-open': open?.kind === 'group' && open.id === g.id ? 'edit' : undefined })}
		>
			{#snippet cell(g: Label, c: Column<Label>)}
				{#if c.key === 'led_colour'}{@render led(g.led_colour)}{/if}
			{/snippet}
			{#snippet actions(g: Label)}
				<button
					type="button"
					class="secondary"
					aria-label="Edit group {g.name}"
					onclick={(e) => {
						const b = e.currentTarget;
						show('group', g.id, () => b);
					}}>Edit</button
				>
			{/snippet}
		</DataTable>
		</div>
	</section>

	{#if asking?.what === 'system'}
		{@const s = asking.system}
		<Confirm title="Delete system {s.label}?" confirmLabel="Delete system {s.label}" modal error={askError} onconfirm={() => deleteSystem(s)} oncancel={() => (asking = undefined)}>
			<p>Its attachments and its rejected-call counts are deleted with it. A system that still has talkgroups or calls can't be deleted. This can't be undone.</p>
		</Confirm>
	{:else if asking?.what === 'detach'}
		{@const a = asking.att}
		<Confirm title="Remove {pairWords(a)}'s attachment?" confirmLabel="Remove the attachment" modal error={askError} onconfirm={() => detach(a)} oncancel={() => (asking = undefined)}>
			<p>From then on its calls are refused, not kept, until it's attached again: what's said meanwhile can't be got back. Calls already kept stay under {a.system_label}.</p>
		</Confirm>
	{/if}

	<p class="meta">{plural(systems.length, 'system')}, {plural(attachments.length, 'attachment')}, {plural(tags.length, 'tag')} and {plural(groups.length, 'group')}.</p>
</AdminPage>

<style>
	section {
		margin-bottom: var(--space-xl);
	}
	.contents {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-xs) var(--space-md);
		margin-bottom: var(--space-md);
	}
	.contents a {
		display: inline-flex;
		align-items: center;
		min-height: var(--target);
	}
	.heading-row {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-sm);
	}
	.intro {
		max-width: var(--measure);
	}
	.meta,
	.none {
		color: var(--muted);
	}
	.meta {
		font-size: var(--text-sm);
	}
	.status {
		margin: 0 0 var(--space-sm);
	}
	.status:empty {
		display: none;
	}
	.pairs {
		list-style: none;
		margin: 0;
		padding: 0;
	}
	.pairs li {
		padding: var(--space-sm) 0;
		border-bottom: var(--border-width) solid var(--border);
	}
	.pairs p {
		margin: 0 0 var(--space-xs);
	}
	.attach {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-xs) var(--space-sm);
	}
	/* The last column's More actions tooltips reach past the page's edge in a wide look: clipped sideways only (ADR 0031). */
	.table-edge {
		overflow-x: clip;
	}
	.attach select {
		width: auto;
		min-width: var(--field-narrow);
		max-width: 100%;
	}
	:global(tr[data-open]) {
		outline: var(--border-width-strong) solid var(--accent);
		outline-offset: calc(-1 * var(--border-width-strong));
	}
</style>
