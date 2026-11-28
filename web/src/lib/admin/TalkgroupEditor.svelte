<script lang="ts">
	// Editing one talkgroup on the Talkgroups page (SPEC.md R22-R24, R30,
	// R50, R100, A25; ADR 0003, 0006, 0031): every R22 field an admin sets,
	// one talkgroup at a time, saved with PATCH as only what changed.
	//
	// - Alert and LED colour say where they're taken from when the talkgroup
	//   has none of its own (null: its tag, then its groups, then its
	//   system, ADR 0006), following the tag and groups as typed, and can be
	//   set back to that.
	// - Saving marks it reviewed (ADR 0003), so an unreviewed talkgroup is
	//   reviewed by saving it, even unchanged.
	// - The form is ArchiveUx section 5's: the Required tag, each problem
	//   under its field and the focus on the first, the top line only
	//   counting them. What the server refuses shows the same way, under its
	//   field: a fireground channel number another talkgroup has names that
	//   talkgroup (A25).
	// - Ignoring, or making a hidden or ignored talkgroup public, asks first
	//   (ArchiveUx's Confirm, inline under the form).
	import { tick } from 'svelte';
	import { Confirm } from 'archiveux/components';
	import { admin, type Access, type AdminError } from './api.ts';
	import {
		alerts,
		asksFirst,
		check,
		fieldOfError,
		fieldOrder,
		formOf,
		groupsOf,
		ledColours,
		MAX_DELAY,
		stateWords,
		states,
		takenFrom,
		titleOf,
		type Field,
		type Label,
		type Patch,
		type Setting,
		type System,
		type TableRow
	} from './talkgroups.ts';

	interface Props {
		talkgroup: TableRow;
		tags: Label[];
		groups: Label[];
		system: System | undefined;
		/** The talkgroup as the admin API answered the save. */
		onsaved: (t: Partial<TableRow>, said: string) => void;
		oncancel: () => void;
		/** The admin API said the admin isn't signed in as one any more. */
		onaccess: (a: Access) => void;
	}
	let { talkgroup, tags, groups, system, onsaved, oncancel, onaccess }: Props = $props();

	// The form starts from the row as it was when it opened.
	// svelte-ignore state_referenced_locally
	let form = $state(formOf(talkgroup));
	let problems: Partial<Record<Field, string>> = $state({});
	let summary = $state('');
	let saving = $state(false);
	let failed = $state('');
	/** A change of state waiting for the admin's answer, with what will be sent. */
	let asking: Patch | undefined = $state();
	let heading: HTMLElement | undefined = $state();

	$effect(() => {
		void tick().then(() => heading?.focus());
	});

	const id = (f: Field) => `tg-${f}`;
	const said = (f: Field, hint = false) => [hint ? `${id(f)}-hint` : '', problems[f] ? `${id(f)}-error` : ''].filter(Boolean).join(' ') || undefined;

	function taken(setting: Setting): string {
		const t = takenFrom(setting, form.tag.trim(), groupsOf(form.groups), tags, groups, system);
		return t ? `Taken from ${t.from}: ${t.value}` : 'Taken from elsewhere: none set';
	}

	async function showProblems(p: Partial<Record<Field, string>>) {
		problems = p;
		const n = Object.keys(p).length;
		summary = n === 1 ? '1 field needs attention' : `${n} fields need attention`;
		await tick();
		const first = fieldOrder.find((f) => p[f]);
		if (first) document.getElementById(id(first))?.focus();
	}

	async function save(e: SubmitEvent) {
		e.preventDefault();
		failed = '';
		asking = undefined;
		const c = check(form, talkgroup);
		if ('problems' in c) {
			await showProblems(c.problems);
			return;
		}
		problems = {};
		summary = '';
		if (Object.keys(c.patch).length === 0 && !talkgroup.unreviewed) {
			onsaved({}, `Nothing to change on ${titleOf(talkgroup)}.`);
			return;
		}
		if (c.patch.state && asksFirst(talkgroup.state, c.patch.state)) {
			asking = c.patch;
			return;
		}
		await send(c.patch);
	}

	async function send(patch: Patch) {
		saving = true;
		const a = await admin<TableRow | AdminError>('PATCH', `/systems/${talkgroup.system_id}/talkgroups/${talkgroup.id}`, patch);
		saving = false;
		if (a.access === 'sign-in' || a.access === 'not-admin') {
			onaccess(a.access);
			return;
		}
		if (a.status >= 400 && a.status < 500 && a.body && 'error' in a.body) {
			asking = undefined;
			const field = fieldOfError(a.body.error);
			if (field) {
				await showProblems({ [field]: a.body.message });
				return;
			}
			failed = a.body.message;
			return;
		}
		if (a.access !== 'open' || !a.body) {
			failed = "Couldn't save just now. Nothing changed. Try again in a minute.";
			if (asking) throw new Error(failed);
			return;
		}
		asking = undefined;
		const t = a.body as TableRow;
		onsaved(t, `Saved ${titleOf(t)}${patch.state ? `: it's ${t.state} now` : ''}.`);
	}

	async function cancelAsking() {
		asking = undefined;
		await tick();
		document.getElementById(id('state'))?.focus();
	}

	const question = $derived(
		asking?.state === 'ignored'
			? { title: `Ignore ${titleOf(talkgroup)}?`, confirm: `Ignore ${titleOf(talkgroup)}`, words: "From now on its calls are refused, not recorded, and can't be got back. The calls it already has stay hidden until retention deletes them." }
			: { title: `Make ${titleOf(talkgroup)} public?`, confirm: `Make ${titleOf(talkgroup)} public`, words: 'Its recordings, and the calls patched with it, can be heard by anyone at once, live and in recent calls.' }
	);
</script>

<section class="editor" aria-labelledby="edit-heading">
	<h2 id="edit-heading" tabindex="-1" bind:this={heading}>Edit {titleOf(talkgroup)}</h2>
	{#if talkgroup.unreviewed}
		<p class="meta">Made by auto-create and not reviewed yet. Saving marks it reviewed.</p>
	{/if}
	<form novalidate onsubmit={save}>
		<p class="error" aria-live="polite">{summary}</p>
		<div class="grid">
			<div class="field">
				<label for={id('label')}>Label</label>
				<input id={id('label')} autocomplete="off" bind:value={form.label} aria-invalid={problems.label ? 'true' : undefined} aria-describedby={said('label', true)} />
				<p class="field-hint" id="{id('label')}-hint">The short alpha tag, shown beside the name.</p>
				{#if problems.label}<p class="field-error" id="{id('label')}-error">{problems.label}</p>{/if}
			</div>
			<div class="field">
				<label for={id('name')}>Name</label>
				<input id={id('name')} autocomplete="off" bind:value={form.name} aria-invalid={problems.name ? 'true' : undefined} aria-describedby={said('name', true)} />
				<p class="field-hint" id="{id('name')}-hint">Listeners see the name, or the label when it's empty.</p>
				{#if problems.name}<p class="field-error" id="{id('name')}-error">{problems.name}</p>{/if}
			</div>
			<div class="field">
				<label for={id('tag')}>Tag <span class="required">required</span></label>
				<input id={id('tag')} autocomplete="off" required list="tg-tags" bind:value={form.tag} aria-invalid={problems.tag ? 'true' : undefined} aria-describedby={said('tag', true)} />
				<datalist id="tg-tags">
					{#each tags as t (t.id)}<option value={t.name}></option>{/each}
				</datalist>
				<p class="field-hint" id="{id('tag')}-hint">A new tag is made when it's first used.</p>
				{#if problems.tag}<p class="field-error" id="{id('tag')}-error">{problems.tag}</p>{/if}
			</div>
			<div class="field">
				<label for={id('groups')}>Groups</label>
				<input id={id('groups')} autocomplete="off" bind:value={form.groups} aria-invalid={problems.groups ? 'true' : undefined} aria-describedby={said('groups', true)} />
				<p class="field-hint" id="{id('groups')}-hint">Separate groups with commas. A new group is made when it's first used.</p>
				{#if problems.groups}<p class="field-error" id="{id('groups')}-error">{problems.groups}</p>{/if}
			</div>
			<div class="field">
				<label for={id('state')}>State</label>
				<select id={id('state')} bind:value={form.state} aria-invalid={problems.state ? 'true' : undefined} aria-describedby={said('state', true)}>
					{#each states as s (s)}<option value={s}>{stateWords[s]}</option>{/each}
				</select>
				<p class="field-hint" id="{id('state')}-hint">Hidden and ignored talkgroups are heard by no listener; an ignored one's calls aren't recorded.</p>
				{#if problems.state}<p class="field-error" id="{id('state')}-error">{problems.state}</p>{/if}
			</div>
			<div class="field">
				<label for={id('type')}>Type</label>
				<input id={id('type')} autocomplete="off" bind:value={form.type} aria-invalid={problems.type ? 'true' : undefined} aria-describedby={said('type', true)} />
				<p class="field-hint" id="{id('type')}-hint">As trunk-recorder's talkgroup list has it, such as D or T.</p>
				{#if problems.type}<p class="field-error" id="{id('type')}-error">{problems.type}</p>{/if}
			</div>
			<div class="field">
				<label for={id('frequency')}>Frequency (MHz)</label>
				<input id={id('frequency')} autocomplete="off" inputmode="decimal" bind:value={form.frequency} aria-invalid={problems.frequency ? 'true' : undefined} aria-describedby={said('frequency')} />
				{#if problems.frequency}<p class="field-error" id="{id('frequency')}-error">{problems.frequency}</p>{/if}
			</div>
			<div class="field">
				<label for={id('sort_order')}>Sort order <span class="required">required</span></label>
				<input id={id('sort_order')} autocomplete="off" inputmode="numeric" required bind:value={form.sort_order} aria-invalid={problems.sort_order ? 'true' : undefined} aria-describedby={said('sort_order', true)} />
				<p class="field-hint" id="{id('sort_order')}-hint">Orders it within its system for listeners; lower first, then by ID.</p>
				{#if problems.sort_order}<p class="field-error" id="{id('sort_order')}-error">{problems.sort_order}</p>{/if}
			</div>
			<div class="field">
				<label for={id('delay_minutes')}>Delay (minutes) <span class="required">required</span></label>
				<input id={id('delay_minutes')} autocomplete="off" inputmode="numeric" required bind:value={form.delay_minutes} aria-invalid={problems.delay_minutes ? 'true' : undefined} aria-describedby={said('delay_minutes', true)} />
				<p class="field-hint" id="{id('delay_minutes')}-hint">0 to {MAX_DELAY.toLocaleString('en-AU')}. Its calls reach listeners this long after each ends; 0 is live.</p>
				{#if problems.delay_minutes}<p class="field-error" id="{id('delay_minutes')}-error">{problems.delay_minutes}</p>{/if}
			</div>
			<div class="field">
				<label for={id('alert')}>Alert</label>
				<select id={id('alert')} bind:value={form.alert} aria-invalid={problems.alert ? 'true' : undefined} aria-describedby={said('alert')}>
					<option value="">{taken('alert')}</option>
					{#each alerts as a (a)}<option value={a}>{a === 'none' ? 'None' : a}</option>{/each}
				</select>
				{#if problems.alert}<p class="field-error" id="{id('alert')}-error">{problems.alert}</p>{/if}
			</div>
			<div class="field">
				<label for={id('led_colour')}>LED colour</label>
				<select id={id('led_colour')} bind:value={form.led_colour} aria-invalid={problems.led_colour ? 'true' : undefined} aria-describedby={said('led_colour')}>
					<option value="">{taken('led_colour')}</option>
					{#each ledColours as c (c)}<option value={c}>{c === 'none' ? 'None' : c}</option>{/each}
				</select>
				{#if problems.led_colour}<p class="field-error" id="{id('led_colour')}-error">{problems.led_colour}</p>{/if}
			</div>
			<div class="field">
				<label for={id('fireground_channel')}>Fireground channel number</label>
				<input
					id={id('fireground_channel')}
					autocomplete="off"
					inputmode="numeric"
					bind:value={form.fireground_channel}
					aria-invalid={problems.fireground_channel ? 'true' : undefined}
					aria-describedby={said('fireground_channel', true)}
				/>
				<p class="field-hint" id="{id('fireground_channel')}-hint">One talkgroup per number, across every system.</p>
				{#if problems.fireground_channel}<p class="field-error" id="{id('fireground_channel')}-error">{problems.fireground_channel}</p>{/if}
			</div>
			<div class="field check">
				<label><input id={id('always_show')} type="checkbox" bind:checked={form.always_show} aria-describedby="{id('always_show')}-hint" /> Always show</label>
				<p class="field-hint" id="{id('always_show')}-hint">Never quiet: listeners see it even when it hasn't been heard for the quiet window. A tag can say this for all its talkgroups.</p>
			</div>
		</div>
		{#if failed}<p class="field-error" role="alert">{failed}</p>{/if}
		<div class="buttons">
			<button type="submit" disabled={saving}>Save</button>
			<button type="button" class="secondary" onclick={oncancel}>Cancel</button>
		</div>
	</form>
	{#if asking}
		<Confirm title={question.title} confirmLabel={question.confirm} level={3} busy={saving} onconfirm={() => send(asking!)} oncancel={cancelAsking}>
			<p>{question.words}</p>
		</Confirm>
	{/if}
</section>

<style>
	.editor {
		margin: 0 0 var(--space-xl);
		padding: var(--space-md);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
		background: var(--surface);
	}
	.editor h2 {
		margin-top: 0;
	}
	.meta {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.error:empty {
		display: none;
	}
	.grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(min(100%, var(--field-wide)), 1fr));
		gap: 0 var(--space-lg);
	}
	.grid input:not([type='checkbox']),
	.grid select {
		width: 100%;
	}
	.check label {
		display: flex;
		align-items: center;
		gap: var(--space-xs);
		min-height: var(--target);
	}
	.buttons {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-sm);
	}
</style>
