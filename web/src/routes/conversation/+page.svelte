<script lang="ts">
	// A shared conversation (SPEC.md P1, P4, R33, R61, R66, R67, R74; ADR
	// 0004, 0015, 0017, 0020): /conversation#<packed> opens its name, its
	// note and its calls as call cards in order, with Play all.
	//
	// - The list is in the fragment, which the browser never sends: the
	//   server serves the app's own page here, the same for every link, and
	//   keeps nothing (R66). The site's generic preview is that page (R74).
	// - The calls come from the call details (POST /api/v1/calls, R67), which
	//   leave out every call the gate holds back. So a hidden, deleted,
	//   delayed or never-stored call is dropped without a word, and the page
	//   is the same as for the link without it (R33, R66).
	// - None left, or not a link Share makes: "This conversation isn't
	//   available", one page whatever the reason, with nothing of the link's
	//   name or note.
	// - The name and note are the sender's own words, in a link anyone can
	//   write: they're shown as text, never as markup.
	import { onDestroy, onMount } from 'svelte';
	import { Icon } from 'archiveux/components';
	import CallCard from '#lib/call/CallCard.svelte';
	import { usePlayer } from '#lib/audio/context.ts';
	import { listeningState } from '#lib/listening/state.svelte.ts';
	import { indexOf, keyOf, resolve, type Catalogue, type Index } from '#lib/listening/set.ts';
	import { sendListeningSet } from '#lib/live/listening-set.ts';
	import { callInfoOf, zoneLabel, type RecentCall } from '#lib/recent/search.ts';
	import { readConversation, type SharedConversation } from '#lib/share/conversation.ts';

	/** How often calls waiting for their recording are asked about again. */
	const POLL_MS = 5000;
	/** The most IDs one call list request names (R73). */
	const PER_REQUEST = 500;

	const listening = listeningState();
	const player = usePlayer();

	let ix: Index | undefined = $state.raw();
	let timeZone = $state('UTC');
	let conv: SharedConversation | undefined = $state.raw();
	// The available calls, in the link's order.
	let calls: RecentCall[] = $state.raw([]);
	let phase: 'loading' | 'failed' | 'done' = $state('loading');

	async function catalogue(): Promise<Index> {
		const r = await fetch('/api/v1/catalogue', { headers: { Accept: 'application/json' } });
		if (!r.ok) throw new Error(String(r.status));
		const c = (await r.json()) as Catalogue & { time_zone?: string };
		const next = indexOf(c);
		listening.settle(next);
		timeZone = c.time_zone || 'UTC';
		return next;
	}

	/** The call details of ids, in their order, the unavailable left out. */
	async function details(ids: string[]): Promise<RecentCall[]> {
		const got = new Map<string, RecentCall>();
		for (let i = 0; i < ids.length; i += PER_REQUEST) {
			const r = await fetch('/api/v1/calls', {
				method: 'POST',
				headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
				body: JSON.stringify({ ids: ids.slice(i, i + PER_REQUEST) })
			});
			if (!r.ok) throw new Error(String(r.status));
			for (const c of ((await r.json()) as { calls: RecentCall[] }).calls) got.set(c.id, c);
		}
		return ids.map((id) => got.get(id)).filter((c): c is RecentCall => !!c);
	}

	let timer: ReturnType<typeof setTimeout> | undefined;
	let asked = 0;
	onDestroy(() => clearTimeout(timer));

	async function load() {
		const n = ++asked;
		clearTimeout(timer);
		phase = 'loading';
		const c = readConversation(location.hash);
		try {
			const [next, got] = await Promise.all([ix ? Promise.resolve(ix) : catalogue(), c ? details(c.calls) : Promise.resolve([])]);
			if (n !== asked) return;
			ix = next;
			conv = c;
			calls = got;
			phase = 'done';
			poll(n);
		} catch {
			if (n === asked) phase = 'failed';
		}
	}

	// Calls in progress, or ended with their recording on its way, are
	// asked about again until each has its recording or isn't available.
	function poll(n: number) {
		if (!calls.some((c) => !c.recording)) return;
		timer = setTimeout(async () => {
			try {
				const got = await details(calls.map((c) => c.id));
				if (n !== asked) return;
				calls = got;
			} catch {
				// Asked again next time.
			}
			if (n === asked) poll(n);
		}, POLL_MS);
	}

	onMount(() => {
		void load();
		// Another link pasted into this tab changes only the fragment.
		const again = () => void load();
		addEventListener('hashchange', again);
		return () => removeEventListener('hashchange', again);
	});

	// The listening set, with the session's additions: what the live feed
	// hears, here as on every page.
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

	const available = $derived(conv && calls.length ? conv : undefined);
	const recorded = $derived(calls.filter((c) => !!c.recording));
	const playingId = $derived(player.replay.call?.callId);

	function playAll() {
		if (!ix || !available) return;
		player.replayAll(
			available.name,
			recorded.map((c) => ({ info: callInfoOf(ix!, c), url: c.recording!.url }))
		);
	}
	function play(c: RecentCall) {
		if (!ix || !c.recording) return;
		player.replayCall(callInfoOf(ix, c), c.recording.url);
	}
	const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;
</script>

<svelte:head>
	<title>{phase !== 'done' ? 'Conversation · RadioArchive' : available ? `${available.name} · RadioArchive` : "Conversation isn't available · RadioArchive"}</title>
</svelte:head>

{#if phase === 'loading'}
	<p class="status" role="status">Loading the conversation…</p>
{:else if phase === 'failed'}
	<p class="error" role="alert">Couldn't load this conversation.</p>
	<button type="button" class="secondary" onclick={load}>Try again</button>
{:else if available && ix}
	<section class="conversation" aria-labelledby="conversation-heading">
		<header class="head">
			<p class="kicker">Shared conversation</p>
			<h1 id="conversation-heading" dir="auto">{available.name}</h1>
			{#if available.note}<p class="note" dir="auto">{available.note}</p>{/if}
			<p class="meta">{plural(calls.length, 'call')}, in order. Times are {zoneLabel(timeZone)} time.</p>
			{#if recorded.length}
				<div class="actions">
					<button type="button" class="play" onclick={playAll}><Icon name="play" /><span>Play all</span></button>
				</div>
			{/if}
		</header>
		<ol class="cards" aria-label="Calls in {available.name}">
			{#each calls as c (c.id)}
				<li><CallCard call={c} {ix} {timeZone} playing={playingId === c.id} onplay={play} /></li>
			{/each}
		</ol>
	</section>
{:else}
	<section class="unavailable" aria-labelledby="conversation-heading">
		<h1 id="conversation-heading">This conversation isn't available</h1>
		<p>Recent calls and live listening are still here.</p>
		<p class="links"><a href="/recent">Recent calls</a><a href="/">Listen</a></p>
	</section>
{/if}

<style>
	.conversation,
	.unavailable {
		max-width: var(--panel-max);
	}
	.head {
		margin-bottom: var(--space-lg);
	}
	.kicker {
		margin: 0 0 var(--space-2xs);
		color: var(--muted);
		font-size: var(--text-sm);
		font-weight: var(--weight-strong);
	}
	h1 {
		margin: 0;
		overflow-wrap: anywhere;
	}
	.note {
		margin: var(--space-sm) 0 0;
		white-space: pre-line;
		overflow-wrap: anywhere;
	}
	.meta {
		margin: var(--space-sm) 0 0;
		color: var(--muted);
		font-family: var(--font-meta);
		font-size: var(--text-sm);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-sm);
		margin-top: var(--space-md);
	}
	.play {
		gap: var(--space-xs);
	}
	.cards {
		display: grid;
		gap: var(--space-md);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.unavailable {
		padding: var(--space-lg);
		background: var(--surface);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
	}
	.links {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-md);
		margin-bottom: 0;
	}
</style>
