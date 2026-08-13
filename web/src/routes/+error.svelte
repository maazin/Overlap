<script lang="ts">
	import { page } from '$app/state';

	// A dead link in a group chat is the most common way someone lands here, so
	// the page says what happened and offers the one action that helps rather
	// than showing a status code.
	const isMissing = $derived(page.status === 404);
</script>

<svelte:head>
	<title>{isMissing ? 'Link not found' : 'Something went wrong'} — Overlap</title>
	<meta name="robots" content="noindex" />
</svelte:head>

<main class="mx-auto max-w-[480px] px-4 py-16">
	<p class="text-muted text-xs font-semibold tracking-[0.09em] uppercase">Overlap</p>
	<h1 class="mt-1.5 mb-2 text-[23px] leading-tight font-semibold tracking-tight">
		{isMissing ? 'That link has expired.' : 'Something went wrong.'}
	</h1>
	<p class="text-muted mb-7 text-sm">
		{#if isMissing}
			Events are kept for 60 days and then cleared. If someone shared this recently, ask them to
			send a fresh link.
		{:else}
			{page.error?.message ?? 'An unexpected error occurred.'}
		{/if}
	</p>

	<a
		href="/new"
		class="bg-ink block w-full rounded-xl p-3.5 text-center text-[15px] font-semibold text-white"
	>
		Start a new one
	</a>
</main>
