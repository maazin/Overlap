<script lang="ts">
	import type { Snippet } from 'svelte';

	/**
	 * The one button in the app.
	 *
	 * Height is floored at the 44px minimum touch target from the guidelines.
	 * Anything smaller is measurably harder to hit accurately on a phone, and
	 * the failure lands on people with less precise motor control first.
	 */
	type Variant = 'primary' | 'secondary' | 'quiet' | 'positive' | 'caution';

	let {
		variant = 'secondary',
		type = 'button',
		disabled = false,
		full = true,
		href = undefined,
		download = false,
		onclick = undefined,
		'aria-label': ariaLabel = undefined,
		children
	}: {
		variant?: Variant;
		type?: 'button' | 'submit';
		disabled?: boolean;
		full?: boolean;
		href?: string;
		download?: boolean;
		onclick?: (e: MouseEvent) => void;
		'aria-label'?: string;
		children: Snippet;
	} = $props();

	const base =
		'inline-flex min-h-touch items-center justify-center gap-2 rounded-[var(--radius-control)] px-5 text-callout font-semibold transition-[background-color,border-color,opacity] duration-150 disabled:opacity-45 disabled:cursor-not-allowed';
</script>

{#snippet inner()}
	{@render children()}
{/snippet}

{#if href}
	<a
		class="{base} {full ? 'w-full' : ''} v-{variant}"
		{href}
		download={download || undefined}
		aria-label={ariaLabel}
	>
		{@render inner()}
	</a>
{:else}
	<button
		class="{base} {full ? 'w-full' : ''} v-{variant}"
		{type}
		{disabled}
		{onclick}
		aria-label={ariaLabel}
	>
		{@render inner()}
	</button>
{/if}

<style>
	.v-primary {
		background: var(--accent);
		color: var(--on-accent);
		border: 1px solid var(--accent);
	}

	.v-secondary {
		background: var(--surface);
		color: var(--ink);
		border: 1px solid var(--hairline-strong);
	}

	.v-quiet {
		background: transparent;
		color: var(--ink-muted);
		border: 1px solid transparent;
	}

	.v-positive {
		background: var(--positive);
		color: var(--on-accent);
		border: 1px solid var(--positive);
	}

	.v-caution {
		background: var(--caution);
		color: var(--on-accent);
		border: 1px solid var(--caution);
	}

	/* Hover is a pointer affordance. Touch devices report no hover, and
	   applying it there leaves a control looking stuck after a tap. */
	@media (hover: hover) {
		.v-primary:hover:not(:disabled) { opacity: 0.88; }
		.v-secondary:hover:not(:disabled) { background: var(--raised); }
		.v-quiet:hover:not(:disabled) { color: var(--ink); }
		.v-positive:hover:not(:disabled) { opacity: 0.9; }
		.v-caution:hover:not(:disabled) { opacity: 0.9; }
	}

	button:active:not(:disabled),
	a:active { opacity: 0.75; }
</style>
