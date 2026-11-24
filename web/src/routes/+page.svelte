<script lang="ts">
	// The Listen page (R84): the listener, with the listening set. For now,
	// only R44's Start listening button: browsers need a user gesture before
	// audio plays, and nothing queues before it's pressed. It does nothing yet.
	function start() {}
</script>

<svelte:head><title>Listen · RadioArchive</title></svelte:head>

<h1>Listen</h1>
<button type="button" class="start" onclick={start}>Start listening</button>

<style>
	.start {
		margin-top: var(--space-lg);
	}
</style>
