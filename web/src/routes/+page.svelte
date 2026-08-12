<script lang="ts">
	import { browser } from '$app/environment';
	import { env } from '$env/dynamic/public';

	const apiURL = env.PUBLIC_API_URL ?? 'http://localhost:8080';

	type Health = { status: string; env: string };

	// The check runs in the browser rather than in a load function on purpose.
	// The API and the web app are deployed to different hosts, so a same-origin
	// server-side fetch would prove nothing about the cross-origin path the real
	// client uses. This way a CORS misconfiguration fails here, on day zero,
	// instead of in phase 2.
	async function check(): Promise<Health> {
		const res = await fetch(`${apiURL}/api/health`);
		if (!res.ok) throw new Error(`HTTP ${res.status}`);
		return res.json();
	}

	// On the server the probe stays pending forever, so SSR emits the "checking"
	// state and hydration replaces it with a real result.
	let probe = $state<Promise<Health>>(browser ? check() : new Promise<Health>(() => {}));
</script>

<svelte:head>
	<title>Overlap</title>
</svelte:head>

<main>
	<p class="eyebrow">Overlap</p>
	<h1>Everyone's free time, in fifteen seconds.</h1>

	<div class="status">
		{#await probe}
			<span class="dot pending"></span>
			<code>api: checking…</code>
		{:then health}
			<span class="dot ok"></span>
			<code>api: {health.status}</code>
			<span class="meta">{health.env}</span>
		{:catch error}
			<span class="dot bad"></span>
			<code>api: unreachable</code>
			<span class="meta">{error.message}</span>
		{/await}
	</div>

	<p class="meta">
		<code>{apiURL}</code>
		<button onclick={() => (probe = check())}>re-check</button>
	</p>
</main>

<style>
	:global(body) {
		margin: 0;
		background: #faf9f7;
		color: #1b1a17;
		font: 16px/1.45 ui-sans-serif, -apple-system, 'Segoe UI', Inter, Roboto, sans-serif;
		-webkit-font-smoothing: antialiased;
	}
	main {
		max-width: 480px;
		margin: 0 auto;
		padding: 64px 16px;
	}
	.eyebrow {
		font-size: 12px;
		letter-spacing: 0.09em;
		text-transform: uppercase;
		color: #6f6b63;
		font-weight: 600;
		margin: 0;
	}
	h1 {
		font-size: 23px;
		line-height: 1.2;
		margin: 6px 0 28px;
		font-weight: 650;
		letter-spacing: -0.02em;
	}
	.status {
		display: flex;
		align-items: center;
		gap: 8px;
		background: #fff;
		border: 1px solid #e6e2dc;
		border-radius: 14px;
		padding: 16px;
	}
	.dot {
		width: 9px;
		height: 9px;
		border-radius: 99px;
		flex: none;
	}
	.dot.ok {
		background: #1a7f5a;
	}
	.dot.bad {
		background: #b4472f;
	}
	.dot.pending {
		background: #a5a099;
	}
	code {
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 13.5px;
	}
	.meta {
		color: #6f6b63;
		font-size: 12.5px;
		display: flex;
		align-items: center;
		gap: 8px;
	}
	button {
		font: inherit;
		font-size: 12.5px;
		background: none;
		border: 1px solid #e6e2dc;
		border-radius: 7px;
		padding: 3px 9px;
		cursor: pointer;
		color: #6f6b63;
	}
</style>
