<script lang="ts">
	// The Recent calls page (SPEC.md R24, R27, R42, R46, R62, R63, R73,
	// R80, R83, R84, section 13; ADR 0008, 0011-0013, 0016): the archive
	// search in the browser.
	//
	// - The list (RecentList.svelte): the listening set's calls by default,
	//   with the talkgroups added for this session (R47), newest first, with
	//   Load more. An empty set is every public talkgroup (ADR 0013).
	// - Filters: public talkgroups (ArchiveUx's Suggest, over the catalogue
	//   the page has), a date and time range (ArchiveUx's DateRange, with
	//   times of day, on the deployment's clock), a unit by radio ID (#83
	//   adds aliases), newest or oldest first. No free text. They live in the
	//   page's address, as chosen, so a filtered list can be linked to.
	// - The search's refusals (R73: over 31 days, too slow) show under the
	//   form as ArchiveUx section 5 says: a field's under that field, the
	//   rest under the form.
	// - Pressing a call replays it in the dock, pausing live listening;
	//   live calls keep queuing meanwhile (R46). Play from here waits on
	//   #75's open question 3.
	// - Share on each row copies the call's link (ADR 0017).
	import { onMount, tick } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { DateRange, Suggest } from 'archiveux/components';
	import RecentList from '#lib/recent/RecentList.svelte';
	import ShareButton from '#lib/share/ShareButton.svelte';
	import AddToBasket from '#lib/basket/AddToBasket.svelte';
	import { usePlayer } from '#lib/audio/context.ts';
	import { listeningState } from '#lib/listening/state.svelte.ts';
	import { indexOf, keyOf, resolve, search as searchTalkgroups, type Catalogue, type Index, type Talkgroup } from '#lib/listening/set.ts';
	import { sendListeningSet } from '#lib/live/listening-set.ts';
	import {
		UNIT,
		chosenOf,
		fieldOf,
		nameOf,
		readFilters,
		startOf,
		searchOf,
		spanOf,
		talkgroupOf,
		writeFilters,
		zoneLabel,
		type Filters,
		type RecentCall,
		type Refusal,
		type Scope
	} from '#lib/recent/search.ts';

	const listening = listeningState();
	const player = usePlayer();

	let ix: Index | undefined = $state.raw();
	let timeZone = $state('UTC');
	let loading: 'loading' | 'failed' | 'done' = $state('loading');

	async function loadCatalogue() {
		loading = 'loading';
		try {
			const r = await fetch('/api/v1/catalogue', { headers: { Accept: 'application/json' } });
			if (!r.ok) throw new Error(String(r.status));
			const c = (await r.json()) as Catalogue & { time_zone?: string };
			const next = indexOf(c);
			listening.settle(next);
			timeZone = c.time_zone || 'UTC';
			ix = next;
			loading = 'done';
		} catch {
			loading = 'failed';
		}
	}
	onMount(loadCatalogue);

	// The listening set, with the session's additions, as the catalogue has
	// it: the list's default, and what the live feed hears (as on Listen).
	const heard = $derived.by(() => {
		if (!ix) return [];
		const seen = new Set<string>();
		return [...resolve(ix, listening.saved), ...resolve(ix, listening.session)]
			.map((r) => r.talkgroup)
			.filter((t) => !seen.has(keyOf(t)) && !!seen.add(keyOf(t)));
	});
	$effect(() => {
		if (!ix) return;
		sendListeningSet(heard.map((t) => ({ system: t.system, id: t.id })));
	});

	// The filters are the address's; the form edits a draft of them.
	const filters = $derived(readFilters(page.url.searchParams));
	const search = $derived(ix ? searchOf(filters, ix, heard, timeZone) : undefined);

	let scope: Scope = $state('set');
	let chosen: Talkgroup[] = $state([]);
	let range = $state({ from: '', to: '', fromTime: '', toTime: '' });
	let unit = $state('');
	let order: 'newest' | 'oldest' = $state('newest');
	let text = $state('');
	$effect(() => {
		const f = filters;
		scope = f.scope;
		chosen = ix ? chosenOf(ix, f) : [];
		range = { from: f.from, to: f.to, fromTime: f.fromTime, toTime: f.toTime };
		unit = f.unit;
		order = f.order;
	});

	let open = $state(false);
	let submitted = false;
	// Problems with the form: the listener's (checked here) and the server's.
	let errors: { talkgroups?: string; dates?: string; unit?: string } = $state({});
	let formError = $state('');
	const problems = $derived(Object.values(errors).filter(Boolean).length);

	async function focusFirst() {
		await tick();
		const first = document.querySelector<HTMLElement>('.filters [aria-invalid="true"], .filters .invalid-group input, .filters .invalid-dates button');
		first?.focus();
	}

	function onrefusal(r: Refusal | undefined) {
		if (!r) {
			errors = {};
			formError = '';
			return;
		}
		const field = fieldOf(r.error);
		if (field) {
			errors = { [field]: r.message };
			formError = '';
			open = true;
			if (submitted) void focusFirst();
		} else {
			errors = {};
			formError = r.message;
		}
		submitted = false;
	}

	function submit(e: SubmitEvent) {
		e.preventDefault();
		const next: typeof errors = {};
		if (scope === 'chosen' && !chosen.length) next.talkgroups = 'Choose at least one talkgroup, or another option above.';
		const u = unit.trim();
		if (u && !UNIT.test(u)) next.unit = 'Enter a radio ID in digits, such as 2001.';
		errors = next;
		formError = '';
		if (Object.keys(next).length) {
			void focusFirst();
			return;
		}
		const f: Filters = { scope, talkgroups: scope === 'chosen' ? chosen.map(spanOf) : [], ...range, unit: u, order };
		const q = writeFilters(f);
		submitted = true;
		void goto(`/recent${q ? `?${q}` : ''}`, { reset: false });
	}

	function clearAll() {
		errors = {};
		formError = '';
		text = '';
		void goto('/recent', { reset: false });
	}

	// Talkgroup suggestions: the catalogue's search (R42), without those chosen.
	function suggest(t: string, caret: number) {
		const word = t.slice(0, caret);
		const taken = new Set(chosen.map(keyOf));
		const found = ix ? searchTalkgroups(ix.catalogue.talkgroups, word).filter((x) => !taken.has(keyOf(x))) : [];
		return {
			target: { start: 0, end: t.length },
			options: found.slice(0, 20).map((x) => ({ kind: 'value' as const, id: keyOf(x), value: `${x.name} (${x.id})`, insert: '', section: x.groups[0] ?? 'Talkgroups' })),
			what: 'talkgroup'
		};
	}
	function pick(o: { kind: string; id?: string }) {
		const t = o.kind === 'value' && ix ? ix.byKey.get(o.id ?? '') : undefined;
		if (t && !chosen.some((c) => keyOf(c) === keyOf(t))) chosen = [...chosen, t];
		scope = 'chosen';
		text = '';
		if (errors.talkgroups) errors = { ...errors, talkgroups: undefined };
	}

	// A call in progress has no recording yet: its talkgroup is heard live,
	// added for the session if it isn't in the set (R47).
	function listenLive(c: RecentCall) {
		const t = ix && talkgroupOf(ix, c);
		if (t && !listening.inSet(t)) listening.addForSession(t);
		if (player.phase === 'stopped') void player.start();
		else if (player.paused) void player.resume();
	}

	const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? '' : 's'}`;
	const dayLabel = (d: string, t: string) => {
		const [y, m, day] = d.split('-').map(Number);
		const s = new Intl.DateTimeFormat('en-AU', { timeZone: 'UTC', day: 'numeric', month: 'short', year: 'numeric' }).format(new Date(Date.UTC(y, m - 1, day)));
		return t ? `${s} ${t}` : s;
	};
	const summary = $derived.by(() => {
		if (!ix) return '';
		const parts: string[] = [];
		if (filters.scope === 'all' || (filters.scope === 'set' && !heard.length)) parts.push('Every talkgroup');
		else if (filters.scope === 'set') parts.push(`Your listening set (${plural(heard.length, 'talkgroup')})`);
		else {
			const ts = chosenOf(ix, filters);
			parts.push(ts.length === 0 ? 'No talkgroups' : ts.length <= 2 ? ts.map((t) => t.name).join(', ') : `${ts[0].name} and ${ts.length - 1} more`);
		}
		if (!filters.from) parts.push('Last 31 days');
		else {
			const to = filters.to || filters.from;
			parts.push(to === filters.from && !filters.fromTime && !filters.toTime ? dayLabel(filters.from, '') : `${dayLabel(filters.from, filters.fromTime)} – ${dayLabel(to, filters.toTime)}`);
		}
		if (filters.unit) parts.push(`Unit ${filters.unit}`);
		parts.push(filters.order === 'oldest' ? 'Oldest first' : 'Newest first');
		return parts.join(' · ');
	});
	const setHint = $derived(
		heard.length
			? `${plural(heard.length, 'talkgroup')}, with any added for this session.`
			: 'Your listening set is empty, so this is every talkgroup.'
	);
</script>

<svelte:head><title>Recent calls · RadioArchive</title></svelte:head>

<h1>Recent calls</h1>

{#if loading === 'loading'}
	<p class="status" role="status">Loading talkgroups…</p>
{:else if loading === 'failed'}
	<p class="error" role="alert">Couldn't load the talkgroups.</p>
	<button type="button" class="secondary" onclick={loadCatalogue}>Try again</button>
{:else if ix}
	<section class="search" aria-label="Filters">
		<div class="bar">
			<button type="button" class="secondary toggle" aria-expanded={open} aria-controls="recent-filters" onclick={() => (open = !open)}>
				<span>Filters</span>
			</button>
			<p class="summary meta" aria-live="polite">{summary}</p>
		</div>

		<form id="recent-filters" class="filters" novalidate hidden={!open} onsubmit={submit}>
			{#if problems}
				<p class="error" aria-live="polite">{problems === 1 ? '1 field needs attention' : `${problems} fields need attention`}</p>
			{/if}

			<fieldset class="field-group field" class:invalid-group={!!errors.talkgroups} aria-describedby="tg-hint{errors.talkgroups ? ' tg-error' : ''}">
				<legend>Talkgroups</legend>
				<p class="field-hint" id="tg-hint">No talkgroups chosen is your listening set.</p>
				<label class="choice"><input type="radio" name="scope" value="set" bind:group={scope} /> Your listening set <span class="hint">{setHint}</span></label>
				<label class="choice"><input type="radio" name="scope" value="all" bind:group={scope} /> Every talkgroup</label>
				<label class="choice"><input type="radio" name="scope" value="chosen" bind:group={scope} /> These talkgroups</label>
				{#if scope === 'chosen'}
					<div class="pick">
						<Suggest bind:text {suggest} onpick={pick} label="Add a talkgroup" showLabel icon="search" placeholder="Name, label, ID or tag" listLabel="Talkgroups" invalid={!!errors.talkgroups} />
						{#if chosen.length}
							<ul class="chosen" aria-label="Chosen talkgroups">
								{#each chosen as t (keyOf(t))}
									<li>
										<span>{t.name}</span>
										<button type="button" class="ghost" aria-label="Remove {t.name}" onclick={() => (chosen = chosen.filter((c) => keyOf(c) !== keyOf(t)))}>Remove</button>
									</li>
								{/each}
							</ul>
						{/if}
					</div>
				{/if}
				{#if errors.talkgroups}<p class="field-error" id="tg-error">{errors.talkgroups}</p>{/if}
			</fieldset>

			<div class="field dates" class:invalid-dates={!!errors.dates} role="group" aria-labelledby="dates-label" aria-describedby="dates-hint{errors.dates ? ' dates-error' : ''}">
				<span class="label" id="dates-label">Date and time</span>
				<div class="date-range">
					<DateRange
						{...range}
						times
						{timeZone}
						onchange={(from, to, fromTime = '', toTime = '') => {
							range = { from, to, fromTime, toTime };
							if (errors.dates) errors = { ...errors, dates: undefined };
						}}
					/>
					{#if range.from}<button type="button" class="ghost" onclick={() => (range = { from: '', to: '', fromTime: '', toTime: '' })}>Any time</button>{/if}
				</div>
				<p class="field-hint" id="dates-hint">Up to 31 days, in {zoneLabel(timeZone)} time. None chosen is the last 31 days.</p>
				{#if errors.dates}<p class="field-error" id="dates-error">{errors.dates}</p>{/if}
			</div>

			<div class="field">
				<label for="recent-unit">Unit</label>
				<input
					id="recent-unit"
					type="text"
					inputmode="numeric"
					autocomplete="off"
					bind:value={unit}
					aria-invalid={errors.unit ? 'true' : undefined}
					aria-describedby="unit-hint{errors.unit ? ' unit-error' : ''}"
					oninput={() => errors.unit && (errors = { ...errors, unit: undefined })}
				/>
				<p class="field-hint" id="unit-hint">By radio ID, such as 2001.</p>
				{#if errors.unit}<p class="field-error" id="unit-error">{errors.unit}</p>{/if}
			</div>

			<div class="field">
				<label for="recent-order">Order</label>
				<select id="recent-order" bind:value={order}>
					<option value="newest">Newest first</option>
					<option value="oldest">Oldest first</option>
				</select>
			</div>

			<div class="actions">
				<button type="submit">Show calls</button>
				<button type="button" class="secondary" onclick={clearAll}>Clear filters</button>
			</div>
		</form>
		{#if formError}<p class="error form-error" role="alert">{formError}</p>{/if}
	</section>

	<p class="meta zone">Times are {zoneLabel(timeZone)} time.</p>
	<RecentList {search} {ix} {timeZone} {player} {onrefusal} onlive={listenLive}>
		{#snippet actions(c: RecentCall)}<ShareButton callId={c.id} label="{nameOf(c)}, {startOf(c.started_at, timeZone)}" /><AddToBasket callId={c.id} label="{nameOf(c)}, {startOf(c.started_at, timeZone)}" />{/snippet}
	</RecentList>
{/if}

<style>
	.search {
		margin: var(--space-lg) 0;
	}
	.bar {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-xs) var(--space-md);
	}
	.summary {
		margin: 0;
		overflow-wrap: anywhere;
	}
	.meta {
		color: var(--muted);
		font-family: var(--font-meta);
		font-size: var(--text-sm);
	}
	.filters {
		margin-top: var(--space-md);
		padding: var(--space-lg);
		max-width: var(--panel-max);
		background: var(--surface);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
	}
	.filters[hidden] {
		display: none;
	}
	.choice {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0 var(--space-xs);
		min-height: var(--target);
		cursor: pointer;
	}
	.choice .hint {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.pick {
		margin: var(--space-xs) 0 0;
	}
	.chosen {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-xs);
		list-style: none;
		margin: var(--space-sm) 0 0;
		padding: 0;
	}
	.chosen li {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2xs);
		padding-left: var(--space-sm);
		background: var(--accent-soft);
		color: var(--accent-soft-fg);
		border-radius: var(--radius);
	}
	.label {
		font-weight: var(--weight-strong);
	}
	.date-range {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-sm);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-sm);
	}
	.form-error {
		margin-top: var(--space-md);
	}
	.zone {
		margin: 0 0 var(--space-sm);
	}
</style>
