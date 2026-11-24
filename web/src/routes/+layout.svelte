<script lang="ts">
	// ArchiveUx's tokens and base styles (R80), before every style of the
	// app's own. The app has no tokens of its own (ArchiveUx rule 1.6).
	import 'archiveux/archiveux.css';
	// The frame, until ArchiveUx's (R81): see its own comment.
	import Frame from '#lib/temporary-frame/Frame.svelte';

	let { children } = $props();
</script>

<Frame>{@render children()}</Frame>
