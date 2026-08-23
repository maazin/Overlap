<script lang="ts">
	import type { Snippet } from 'svelte';

	/**
	 * A grouped block of related content.
	 *
	 * State is carried either by a deep ground with light text, or by an
	 * outline with coloured text on the ordinary surface. Pale tinted fills are
	 * avoided throughout: a washed-out hue is what makes an interface look
	 * generic, and it also tends to fall below contrast minimums.
	 */
	let {
		tone = 'plain',
		padded = true,
		children
	}: {
		tone?: 'plain' | 'positive' | 'caution' | 'critical' | 'accent';
		padded?: boolean;
		children: Snippet;
	} = $props();
</script>

<section class="card t-{tone} {padded ? 'p-5' : ''}">
	{@render children()}
</section>

<style>
	.card {
		border-radius: var(--radius-card);
		border: 1px solid var(--hairline);
		background: var(--surface);
		box-shadow: var(--shadow-card);
	}

	/* Deep grounds. */
	.t-accent {
		background: var(--accent);
		border-color: var(--accent);
		color: var(--on-accent);
		box-shadow: none;
	}

	.t-positive {
		background: var(--positive-deep);
		border-color: var(--positive-deep);
		color: var(--on-deep);
		box-shadow: none;
	}

	/* Outlines. */
	.t-caution {
		border-color: var(--caution-line);
		color: var(--caution);
	}

	.t-critical {
		border-color: var(--critical-line);
		color: var(--critical);
	}
</style>
