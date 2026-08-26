<script lang="ts">
	/**
	 * A wait that explains itself once it stops being brief.
	 *
	 * On a free host the API sleeps when nobody is using it, and the first
	 * request after a quiet spell waits out a container start that can take the
	 * better part of a minute. Held at one word, that wait is indistinguishable
	 * from a broken page, and the person staring at it is usually the first
	 * person to tap a link somebody pasted into a group chat. They have no
	 * reason to believe the thing works, so silence reads as proof it does not.
	 *
	 * Saying what is happening does not make it faster. It does make it
	 * something to wait through rather than something to give up on.
	 */
	let { slowAfterMs = 4000 }: { slowAfterMs?: number } = $props();

	let slow = $state(false);

	$effect(() => {
		const timer = setTimeout(() => (slow = true), slowAfterMs);
		return () => clearTimeout(timer);
	});
</script>

<div class="py-20 text-center">
	<p class="m-0 text-subhead u-faint" aria-live="polite">Loading</p>
	{#if slow}
		<p class="mx-auto mt-3 mb-0 max-w-sm text-footnote u-faint">
			Taking longer than usual. The server sleeps when nobody is using it, so the first visit
			after a quiet spell waits for it to start.
		</p>
	{/if}
</div>
