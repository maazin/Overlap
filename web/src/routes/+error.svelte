<script lang="ts">
	import { page } from '$app/state';
	import Button from '$lib/ui/Button.svelte';

	// A dead link pasted into a group chat is the usual way somebody lands
	// here, so the page says what happened and offers the one action that
	// helps rather than showing a status code.
	const isMissing = $derived(page.status === 404);
</script>

<svelte:head>
	<title>{isMissing ? 'Link not found' : 'Something went wrong'}</title>
	<meta name="robots" content="noindex" />
</svelte:head>

<main class="u-column py-20">
	<p class="m-0 text-caption font-semibold tracking-[0.14em] uppercase u-faint">Overlap</p>
	<h1 class="u-serif mt-3 mb-3 text-title font-semibold">
		{isMissing ? 'That link has expired.' : 'Something went wrong.'}
	</h1>
	<p class="mt-0 mb-8 text-body u-muted">
		{#if isMissing}
			Events are kept for 60 days and then cleared. If somebody shared this recently, ask them to
			send a fresh link.
		{:else}
			{page.error?.message ?? 'An unexpected error occurred.'}
		{/if}
	</p>

	<Button variant="primary" href="/new">Start a new one</Button>
</main>
