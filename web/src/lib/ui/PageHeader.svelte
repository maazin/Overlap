<script lang="ts">
	import type { Snippet } from 'svelte';

	/**
	 * The masthead every screen opens with.
	 *
	 * The title uses the serif face. It gives the product a settled, printed
	 * voice and separates the name of a thing from the controls that act on it,
	 * without spending a colour to do the same job.
	 */
	let {
		eyebrow = 'Overlap',
		title,
		children = undefined
	}: { eyebrow?: string; title: string; children?: Snippet } = $props();
</script>

<header class="mb-6">
	<p class="m-0 text-caption font-semibold tracking-[0.14em] uppercase u-faint">{eyebrow}</p>
	<h1 class="u-serif mt-2 mb-0 text-display font-semibold">{title}</h1>
	{#if children}
		<p class="mt-2 mb-0 text-subhead u-muted">{@render children()}</p>
	{/if}
</header>
