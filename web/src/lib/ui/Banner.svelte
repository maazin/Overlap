<script lang="ts">
	import type { Snippet } from 'svelte';
	import Card from './Card.svelte';

	/**
	 * A short verdict with its reasoning.
	 *
	 * The state is named in words as well as shown in colour. Someone with a
	 * colour vision deficiency, or anyone reading a greyscale screenshot, gets
	 * the same information from the label that everyone else gets from the
	 * ground it sits on.
	 */
	let {
		tone = 'plain',
		label = '',
		title,
		body = '',
		children = undefined
	}: {
		tone?: 'plain' | 'positive' | 'caution' | 'critical' | 'accent';
		label?: string;
		title: string;
		body?: string;
		children?: Snippet;
	} = $props();
</script>

<Card {tone}>
	{#if label}
		<p class="m-0 mb-2 text-caption font-semibold tracking-[0.14em] uppercase opacity-70">
			{label}
		</p>
	{/if}
	<h2 class="m-0 text-heading font-semibold tracking-tight">{title}</h2>
	{#if body}
		<p class="mt-2 mb-0 text-subhead opacity-80">{body}</p>
	{/if}
	{#if children}
		<div class="mt-4">{@render children()}</div>
	{/if}
</Card>
