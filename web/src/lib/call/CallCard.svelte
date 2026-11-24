<script lang="ts">
	// A call card in a list of them: a shared conversation's (SPEC.md R61,
	// R66; ADR 0017, ADR 0020). As the call link's card, smaller: the
	// talkgroup's name (R24) and group, the start on the deployment's clock
	// (TZ), the duration, the units heard, Play, Download, Share and Add to
	// basket, and a link to the call's own page (where Listen live is, for a
	// call in progress). It shows only what the call details gave, so a
	// call the gate holds back never reaches it.
	import { Icon } from 'archiveux/components';
	import ShareButton from '#lib/share/ShareButton.svelte';
	import AddToBasket from '#lib/basket/AddToBasket.svelte';
	import { callPath } from '#lib/share/link.ts';
	import type { Index } from '#lib/listening/set.ts';
	import { clock, dateTimeOf, nameOf, startOf, talkgroupOf, unitsOf, zoneLabel, type RecentCall } from '#lib/recent/search.ts';

	let {
		call,
		ix,
		timeZone,
		playing = false,
		onplay
	}: {
		call: RecentCall;
		ix: Index;
		timeZone: string;
		/** This call is the one replaying in the dock. */
		playing?: boolean;
		onplay: (c: RecentCall) => void;
	} = $props();

	const t = $derived(talkgroupOf(ix, call));
	const name = $derived(nameOf(call));
	const units = $derived(unitsOf(call));
	const said = $derived(`${name}, ${startOf(call.started_at, timeZone)}`);
</script>

<article class="call-card" aria-labelledby="card-{call.id}" data-call-id={call.id} data-state={call.in_progress ? 'in-progress' : call.recording ? 'recorded' : 'ended'}>
	<h2 id="card-{call.id}">
		{#if t?.led_colour}<span class="led" data-led={t.led_colour} aria-hidden="true"></span>{/if}
		<span class="tg">{name}</span>
	</h2>
	{#if t?.groups[0] || !ix.single}
		<p class="where">
			{#if t?.groups[0]}<span class="group">{t.groups.join(', ')}</span>{/if}
			{#if !ix.single}<span class="system">{call.system.name || call.system.label}</span>{/if}
		</p>
	{/if}
	<dl class="facts">
		<div>
			<dt>Started</dt>
			<dd><time datetime={call.started_at}>{dateTimeOf(call.started_at, timeZone)}</time> <span class="zone">{zoneLabel(timeZone)} time</span></dd>
		</div>
		<div>
			<dt>Duration</dt>
			<dd>{#if call.in_progress}<span class="progress">In progress</span>{:else}{clock(call.duration_ms ?? 0)}{/if}</dd>
		</div>
		<div>
			<dt>{units.length === 1 ? 'Unit heard' : 'Units heard'}</dt>
			<dd class="units">{units.length ? units.join(', ') : 'None heard'}</dd>
		</div>
	</dl>
	<div class="actions">
		{#if call.recording}
			<button type="button" class="play" aria-label="{playing ? 'Play again' : 'Play'}: {said}" onclick={() => onplay(call)}>
				<Icon name="play" /><span>{playing ? 'Play again' : 'Play'}</span>
			</button>
			<a class="button secondary" href="/api/v1/calls/{encodeURIComponent(call.id)}/download" download aria-label="Download: {said}"><span>Download</span></a>
		{/if}
		<ShareButton callId={call.id} label={said} />
		<AddToBasket callId={call.id} label={said} />
		<a class="open" href={callPath(call.id)} aria-label="Open the call: {said}">Open the call</a>
	</div>
	{#if call.in_progress}
		<p class="note">In progress. Open the call to listen live; once it ends, it plays here.</p>
	{:else if !call.recording}
		<p class="note">The call has ended. Its recording is on its way.</p>
	{/if}
</article>

<style>
	.call-card {
		padding: var(--space-md) var(--space-lg);
		background: var(--surface);
		border: var(--border-width) solid var(--border);
		border-radius: var(--radius);
	}
	h2 {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-xs);
		margin: 0;
		font-size: var(--text-lg);
		overflow-wrap: anywhere;
	}
	.where {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-xs);
		margin: var(--space-2xs) 0 0;
		color: var(--muted);
	}
	.where .group + .system::before {
		content: '·';
		margin-right: var(--space-xs);
	}
	.facts {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-sm) var(--space-lg);
		margin: var(--space-sm) 0 var(--space-md);
	}
	.facts div {
		display: grid;
		gap: var(--space-2xs);
		min-width: 0;
	}
	.facts dt {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.facts dd {
		margin: 0;
		font-variant-numeric: tabular-nums;
		overflow-wrap: anywhere;
	}
	.zone {
		color: var(--muted);
		font-size: var(--text-sm);
	}
	.progress {
		padding: var(--badge-pad-y) var(--badge-pad-x);
		border-radius: var(--radius-badge);
		background: var(--accent-soft);
		color: var(--accent-soft-fg);
		font-weight: var(--weight-strong);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-xs) var(--space-sm);
	}
	.play {
		gap: var(--space-xs);
	}
	.open {
		display: inline-flex;
		align-items: center;
		min-height: var(--target);
	}
	.note {
		margin: var(--space-sm) 0 0;
		color: var(--muted);
	}
</style>
