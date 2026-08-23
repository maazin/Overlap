<script lang="ts">
	import { browser } from '$app/environment';
	import { API_URL } from '$lib/api';
	import Button from '$lib/ui/Button.svelte';
	import Card from '$lib/ui/Card.svelte';

	type Health = { status: string; env: string };

	// Runs in the browser rather than in a load function on purpose. The API and
	// the web app sit on different hosts, so a same-origin server fetch would
	// prove nothing about the cross-origin path a real visitor uses.
	async function check(): Promise<Health> {
		const res = await fetch(`${API_URL}/api/health`);
		if (!res.ok) throw new Error(`HTTP ${res.status}`);
		return res.json();
	}

	let probe = $state<Promise<Health>>(browser ? check() : new Promise<Health>(() => {}));

	const title = "Overlap, everyone's free time in fifteen seconds";
	const description =
		'Group scheduling without the grid. Tap three buttons, or connect a calendar. No account needed.';
</script>

<svelte:head>
	<title>{title}</title>
	<meta name="description" content={description} />
	<meta property="og:type" content="website" />
	<meta property="og:site_name" content="Overlap" />
	<meta property="og:title" content={title} />
	<meta property="og:description" content={description} />
	<meta name="twitter:card" content="summary" />
</svelte:head>

<main class="u-column py-14">
	<p class="m-0 text-caption font-semibold tracking-[0.14em] uppercase u-faint">Overlap</p>
	<h1 class="u-serif mt-3 mb-0 text-[2.5rem] leading-[1.05] font-semibold">
		Everyone's free time, in fifteen seconds.
	</h1>
	<p class="mt-4 mb-8 text-body u-muted">
		Tap three buttons. No grid to paint, no account, no app to install. Most people finish before
		they would have finished reading a When2Meet.
	</p>

	<Button variant="primary" href="/new">Create an event</Button>
	<p class="mt-3 mb-0 text-center text-footnote u-faint">
		You get a link. Paste it into the group chat.
	</p>

	<!-- Dominance sits on the second screen. Speed earns the first visit, and
	     this is what brings somebody back. -->
	<section class="mt-16 border-t pt-10 u-hairline">
		<h2 class="u-serif m-0 text-title font-semibold">It tells you when to stop waiting.</h2>
		<p class="mt-3 mb-6 text-callout u-muted">
			Four of six have replied and you have no idea whether the last two matter. Overlap works that
			out. Usually they do not.
		</p>

		<div class="mb-3">
			<Card tone="accent">
				<p class="m-0 mb-2 text-caption font-semibold tracking-[0.14em] uppercase opacity-60">
					Ready to decide
				</p>
				<p class="m-0 text-callout font-semibold tracking-tight">
					Thursday 3pm wins whatever anyone else says.
				</p>
				<p class="mt-2 mb-0 text-subhead opacity-75">
					Dev and Cara have not replied, and no answer they could give changes the order.
				</p>
			</Card>
		</div>

		<Card tone="caution">
			<p class="m-0 mb-2 text-caption font-semibold tracking-[0.14em] uppercase opacity-80">
				Blocked
			</p>
			<p class="m-0 text-callout font-semibold tracking-tight">Waiting on Sam.</p>
			<p class="mt-2 mb-0 text-subhead opacity-90">
				Sam is required and has not replied. Nothing can be settled until they do, and nobody else
				needs chasing.
			</p>
		</Card>

		<p class="mt-6 mb-0 text-callout u-muted">
			It also knows the difference between a time that works and a time someone will resent, keeps
			required people from being outvoted by bystanders, and finishes the job with a locked time and
			a calendar invite. Connecting a calendar reads your free and busy hours only, never what any
			of it is for.
		</p>
	</section>

	<div class="mt-12">
		<Card>
			<div class="flex items-center gap-2.5">
				{#await probe}
					<span class="mark" style="background: var(--ink-faint)" aria-hidden="true"></span>
					<code class="font-mono text-footnote u-muted">api: checking</code>
				{:then health}
					<span class="mark" style="background: var(--positive)" aria-hidden="true"></span>
					<code class="font-mono text-footnote">api: {health.status}</code>
					<span class="text-footnote u-faint">{health.env}</span>
				{:catch err}
					<span class="mark" style="background: var(--critical)" aria-hidden="true"></span>
					<code class="font-mono text-footnote">api: unreachable</code>
					<span class="text-footnote u-faint">{err.message}</span>
				{/await}
			</div>
		</Card>
	</div>
</main>

<style>
	.mark {
		width: 0.5rem;
		height: 0.5rem;
		flex: none;
	}
</style>
