<script lang="ts">
	// Editing a system, a tag or a group on the Systems page (SPEC.md R11,
	// R21, R22; ADR 0003, 0006, 0034), in a panel above its table as the
	// Talkgroups page's editor is (ADR 0031): a new system or group's every
	// field, or only what changed (PATCH).
	//
	// - A system: its ID (fixed once made), label, name, auto-create (off by
	//   default; switching it on asks first, inline, since the talkgroups it
	//   makes are public at once), alert, LED colour and sort order.
	// - A tag or a group: its name, alert, LED colour and sort order. Their
	//   alert and LED colour are what their talkgroups with none of their own
	//   take (ADR 0006: the tag's, then the groups', then the system's).
	// - The form is ArchiveUx section 5's: problems under their fields, the
	//   focus on the first, the top line counting them; the server's
	//   refusals the same way, by their code.
	import { tick } from 'svelte';
	import { Confirm } from 'archiveux/components';
	import { admin, type Access, type AdminError } from './api.ts';
	import { alerts, ledColours, type Label } from './talkgroups.ts';
	import { check, fieldOfError, fieldOrder, formOf, type Body, type Field, type FullSystem, type Kind } from './systems.ts';

	interface Props {
		kind: Kind;
		/** The thing as it is, or undefined for a new one. */
		thing: FullSystem | Label | undefined;
		onsaved: (t: FullSystem | Label, said: string) => void;
		oncancel: () => void;
		onaccess: (a: Access) => void;
	}
	let { kind, thing, onsaved, oncancel, onaccess }: Props = $props();

	// svelte-ignore state_referenced_locally
	let form = $state(formOf(thing));
	let problems: Partial<Record<Field, string>> = $state({});
	let summary = $state('');
	let saving = $state(false);
	let failed = $state('');
	/** Switching auto-create on, waiting for the admin's answer. */
	let asking: Body | undefined = $state();
	let heading: HTMLElement | undefined = $state();

	$effect(() => {
		void tick().then(() => heading?.focus());
	});

	const p = $derived(`${kind}-edit`);
	const id = (f: Field) => `${p}-${f}`;
	const said = (f: Field, hint = false) => [hint ? `${id(f)}-hint` : '', problems[f] ? `${id(f)}-error` : ''].filter(Boolean).join(' ') || undefined;

	const nameOfThing = (t: FullSystem | Label) => ('label' in t ? t.label : t.name);
	const title = $derived(thing ? `Edit ${kind} ${nameOfThing(thing)}` : `Add a ${kind}`);
	const unset = $derived(
		kind === 'system' ? 'Not set' : kind === 'tag' ? "Not set: its talkgroups go on to their groups' or system's" : "Not set: its talkgroups go on to their other groups' or system's"
	);

	async function showProblems(ps: Partial<Record<Field, string>>) {
		problems = ps;
		const n = Object.keys(ps).length;
		summary = n === 1 ? '1 field needs attention' : `${n} fields need attention`;
		await tick();
		const first = fieldOrder.find((f) => ps[f]);
		if (first) document.getElementById(id(first))?.focus();
	}

	async function save(e: SubmitEvent) {
		e.preventDefault();
		failed = '';
		asking = undefined;
		const c = check(kind, form, thing);
		if ('problems' in c) {
			await showProblems(c.problems);
			return;
		}
		problems = {};
		summary = '';
		if (thing && Object.keys(c.body).length === 0) {
			onsaved(thing, `Nothing to change on ${nameOfThing(thing)}.`);
			return;
		}
		if (kind === 'system' && c.body.auto_create === true) {
			asking = c.body;
			return;
		}
		await send(c.body);
	}

	async function send(body: Body) {
		saving = true;
		const path = thing ? `/${kind}s/${thing.id}` : `/${kind}s`;
		const a = await admin<FullSystem | Label | AdminError>(thing ? 'PATCH' : 'POST', path, body);
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
		if (a.access !== 'open' || !a.body || 'error' in a.body) {
			failed = "Couldn't save just now. Nothing changed. Try again in a minute.";
			if (asking) throw new Error(failed);
			return;
		}
		asking = undefined;
		const t = a.body;
		const auto = 'auto_create' in t && body.auto_create !== undefined ? `: auto-create is ${t.auto_create ? 'on' : 'off'}` : '';
		onsaved(t, `${thing ? 'Saved' : 'Added'} ${kind} ${nameOfThing(t)}${auto}.`);
	}

	async function cancelAsking() {
		asking = undefined;
		await tick();
		document.getElementById(id('auto_create'))?.focus();
	}
</script>

<section class="editor" aria-labelledby="{p}-heading">
	<h2 id="{p}-heading" tabindex="-1" bind:this={heading}>{title}</h2>
	<form novalidate onsubmit={save}>
		<p class="error" aria-live="polite">{summary}</p>
		<div class="grid">
			{#if kind === 'system'}
				{#if !thing}
					<div class="field">
						<label for={id('id')}>ID <span class="required">required</span></label>
						<input id={id('id')} autocomplete="off" inputmode="numeric" required bind:value={form.id} aria-invalid={problems.id ? 'true' : undefined} aria-describedby={said('id', true)} />
						<p class="field-hint" id="{id('id')}-hint">The system's own number, which an import merges on. It can't be changed later.</p>
						{#if problems.id}<p class="field-error" id="{id('id')}-error">{problems.id}</p>{/if}
					</div>
				{/if}
				<div class="field">
					<label for={id('label')}>Label <span class="required">required</span></label>
					<input id={id('label')} autocomplete="off" required bind:value={form.label} aria-invalid={problems.label ? 'true' : undefined} aria-describedby={said('label', true)} />
					<p class="field-hint" id="{id('label')}-hint">Short, such as EX. Listeners see it when there are several systems.</p>
					{#if problems.label}<p class="field-error" id="{id('label')}-error">{problems.label}</p>{/if}
				</div>
			{/if}
			<div class="field">
				<label for={id('name')}>Name{#if kind !== 'system'}{' '}<span class="required">required</span>{/if}</label>
				<input id={id('name')} autocomplete="off" required={kind !== 'system'} bind:value={form.name} aria-invalid={problems.name ? 'true' : undefined} aria-describedby={said('name', kind === 'system')} />
				{#if kind === 'system'}<p class="field-hint" id="{id('name')}-hint">The longer name, such as Example network.</p>{/if}
				{#if problems.name}<p class="field-error" id="{id('name')}-error">{problems.name}</p>{/if}
			</div>
			<div class="field">
				<label for={id('alert')}>Alert</label>
				<select id={id('alert')} bind:value={form.alert} aria-invalid={problems.alert ? 'true' : undefined} aria-describedby={said('alert')}>
					<option value="">{unset}</option>
					{#each alerts as a (a)}<option value={a}>{a === 'none' ? 'None' : a}</option>{/each}
				</select>
				{#if problems.alert}<p class="field-error" id="{id('alert')}-error">{problems.alert}</p>{/if}
			</div>
			<div class="field">
				<label for={id('led_colour')}>LED colour</label>
				<select id={id('led_colour')} bind:value={form.led_colour} aria-invalid={problems.led_colour ? 'true' : undefined} aria-describedby={said('led_colour')}>
					<option value="">{unset}</option>
					{#each ledColours as c (c)}<option value={c}>{c === 'none' ? 'None' : c}</option>{/each}
				</select>
				{#if problems.led_colour}<p class="field-error" id="{id('led_colour')}-error">{problems.led_colour}</p>{/if}
			</div>
			<div class="field">
				<label for={id('sort_order')}>Sort order <span class="required">required</span></label>
				<input id={id('sort_order')} autocomplete="off" inputmode="numeric" required bind:value={form.sort_order} aria-invalid={problems.sort_order ? 'true' : undefined} aria-describedby={said('sort_order', true)} />
				<p class="field-hint" id="{id('sort_order')}-hint">Orders it among the {kind}s listeners see; lower first, then by {kind === 'system' ? 'ID' : 'name'}.</p>
				{#if problems.sort_order}<p class="field-error" id="{id('sort_order')}-error">{problems.sort_order}</p>{/if}
			</div>
			{#if kind === 'system'}
				<div class="field check">
					<label><input id={id('auto_create')} type="checkbox" bind:checked={form.auto_create} aria-describedby="{id('auto_create')}-hint" /> Auto-create talkgroups</label>
					<p class="field-hint" id="{id('auto_create')}-hint">Off by default. On, a call on a talkgroup this system doesn't list creates it, public and unreviewed; off, the call is refused and counted.</p>
				</div>
			{/if}
		</div>
		{#if failed}<p class="field-error" role="alert">{failed}</p>{/if}
		<div class="buttons">
			<button type="submit" disabled={saving}>{thing ? 'Save' : `Add the ${kind}`}</button>
			<button type="button" class="secondary" onclick={oncancel}>Cancel</button>
		</div>
	</form>
	{#if asking}
		{@const label = form.label.trim()}
		<Confirm title="Switch auto-create on for {label}?" confirmLabel="Switch auto-create on" level={3} busy={saving} onconfirm={() => send(asking!)} oncancel={cancelAsking}>
			<p>From then on, a call on a talkgroup {label} doesn't list creates it, public and unreviewed, and anyone can hear it at once, live and in recent calls. Review new talkgroups on the Talkgroups page.</p>
		</Confirm>
	{/if}
</section>

<style>
	.editor {
		margin: 0 0 var(--space-lg);
		padding: var(--space-md);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
		background: var(--surface);
	}
	.editor h2 {
		margin-top: 0;
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
