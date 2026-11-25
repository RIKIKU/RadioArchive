<script lang="ts">
	// A talkgroup's recordings, from its row on the Talkgroups page (SPEC.md
	// R34, A13; ADR 0026, 0031): its calls newest first, a hundred at a time,
	// hidden and ignored ones too, each played from its recording through
	// the admin API.
	//
	// It plays in the page's own player (a Replay of its own, ADR 0016's
	// output), never the listener's queue or dock: an admin never hears a
	// hidden talkgroup in the listener (R34). Playback isn't audited.
	import { onDestroy, tick, untrack } from 'svelte';
	import { StateBadge } from 'archiveux/components';
	import { Replay } from '#lib/audio/replay.svelte.ts';
	import { admin, whenOf } from './api.ts';
	import { nameOf, stateWords, titleOf, type State, type TableRow } from './talkgroups.ts';

	interface AdminCall {
		id: string;
		talkgroup: number;
		patched_talkgroups: number[];
		state: State;
		started_at: string;
		ended_at: string | null;
		in_progress: boolean;
		recording: { url: string; size: number } | null;
	}
	interface Calls {
		calls: AdminCall[];
		next: string | null;
	}

	let { talkgroup, timeZone, onclose }: { talkgroup: TableRow; timeZone: string; onclose: () => void } = $props();

	const replay = new Replay();
	let calls: AdminCall[] = $state([]);
	let next: string | null = $state(null);
	let loading = $state(true);
	let failed = $state('');
	let said = $state('');
	let heading: HTMLElement | undefined = $state();

	const path = $derived(`/systems/${talkgroup.system_id}/talkgroups/${talkgroup.id}/calls`);

	async function load(cursor?: string) {
		loading = true;
		failed = '';
		const a = await admin<Calls>('GET', cursor ? `${path}?cursor=${encodeURIComponent(cursor)}` : path);
		loading = false;
		if (a.access !== 'open' || !a.body) {
			failed = a.status === 404 ? 'This talkgroup is gone.' : "Couldn't load its calls just now. Try again in a minute.";
			return;
		}
		calls = cursor ? [...calls, ...a.body.calls] : a.body.calls;
		next = a.body.next;
	}

	// Another talkgroup's recordings in the same place: what was playing stops.
	$effect(() => {
		void path;
		untrack(() => {
			replay.stop();
			calls = [];
			next = null;
			said = '';
			void load();
			void tick().then(() => heading?.focus());
		});
	});
	onDestroy(() => replay.stop());

	const when = (c: AdminCall) => whenOf(c.started_at, timeZone);
	function length(c: AdminCall): string {
		if (!c.ended_at) return '';
		const s = Math.max(0, Math.round((new Date(c.ended_at).getTime() - new Date(c.started_at).getTime()) / 1000));
		return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
	}
	const clock = (s: number) => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;

	function play(c: AdminCall) {
		if (!c.recording) return;
		if (replay.call?.callId === c.id) {
			replay.stop();
			said = 'Stopped.';
			return;
		}
		replay.play(
			{
				callId: c.id,
				talkgroup: { id: talkgroup.id, label: talkgroup.label, name: nameOf(talkgroup) },
				unit: null,
				startedAt: c.started_at,
				source: 'recording'
			},
			c.recording.url
		);
		said = `Started the call of ${when(c)}.`;
	}

	$effect(() => {
		if (replay.failure) said = replay.failure;
	});
</script>

<section class="calls" aria-labelledby="calls-heading">
	<div class="head">
		<h2 id="calls-heading" tabindex="-1" bind:this={heading}>Recordings: {titleOf(talkgroup)}</h2>
		<button type="button" class="secondary" onclick={onclose}>Close</button>
	</div>
	<p class="meta">
		Its calls and calls patched with it, newest first, whatever their state. They play here only, never in the listener.{talkgroup.state !== 'public' ? ` Listeners can't hear any of them while it's ${talkgroup.state}.` : ''}
	</p>
	<p class="said" role="status">{said}</p>
	{#if replay.call}
		<p class="playing">Playing the call of {whenOf(replay.call.startedAt, timeZone)}: {clock(replay.elapsed)}</p>
	{/if}
	{#if failed}
		<p class="field-error" role="alert">{failed}</p>
	{:else if loading && calls.length === 0}
		<p class="meta">Loading…</p>
	{:else if calls.length === 0}
		<p class="meta">No calls kept for this talkgroup.</p>
	{:else}
		<ul class="list" aria-label="Calls on {titleOf(talkgroup)}">
			{#each calls as c (c.id)}
				<li>
					<span class="when">{when(c)}</span>
					{#if c.in_progress}
						<span class="meta">In progress, no recording yet</span>
					{:else}
						<span class="meta">{length(c)}</span>
					{/if}
					{#if c.talkgroup !== talkgroup.id}<span class="meta">Patched from TG {c.talkgroup}</span>{/if}
					<StateBadge state={c.state === 'public' ? 'ok' : 'neutral'} text={stateWords[c.state]} />
					{#if c.recording}
						<button type="button" class="secondary play" aria-pressed={replay.call?.callId === c.id} onclick={() => play(c)}>
							{replay.call?.callId === c.id ? 'Stop' : 'Play'}<span class="visually-hidden"> the call of {when(c)}</span>
						</button>
					{/if}
				</li>
			{/each}
		</ul>
		{#if next}
			<button type="button" class="secondary" disabled={loading} onclick={() => load(next!)}>Show older calls</button>
		{/if}
	{/if}
</section>

<style>
	.calls {
		max-width: var(--measure);
		margin: 0 0 var(--space-xl);
		padding: var(--space-md);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
		background: var(--surface);
	}
	.head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-sm);
		flex-wrap: wrap;
	}
	.head h2 {
		margin: 0;
	}
	.meta {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.said:empty {
		display: none;
	}
	.playing {
		font-variant-numeric: tabular-nums;
	}
	.list {
		list-style: none;
		margin: 0 0 var(--space-md);
		padding: 0;
	}
	.list li {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: var(--space-xs) var(--space-sm);
		padding: var(--space-xs) 0;
		border-bottom: var(--border-width) solid var(--border);
	}
	.when {
		font-variant-numeric: tabular-nums;
	}
	.play {
		margin-inline-start: auto;
	}
</style>
