<script lang="ts">
	import { browser } from '$app/environment';
	import { API_URL } from '$lib/api';

	type Health = { status: string; env: string };

	// The check runs in the browser rather than in a load function on purpose.
	// The API and the web app are deployed to different hosts, so a same-origin
	// server-side fetch would prove nothing about the cross-origin path the real
	// client uses.
	async function check(): Promise<Health> {
		const res = await fetch(`${API_URL}/api/health`);
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

<main class="mx-auto max-w-[480px] px-4 py-16">
	<p class="text-muted text-xs font-semibold tracking-[0.09em] uppercase">Overlap</p>
	<h1 class="mt-1.5 mb-1 text-[23px] leading-tight font-semibold tracking-tight">
		Everyone's free time, in fifteen seconds.
	</h1>
	<p class="text-muted mb-7 text-sm">Tap three buttons. No grid to paint.</p>

	<a
		href="/new"
		class="bg-ink block w-full rounded-xl p-3.5 text-center text-[15px] font-semibold text-white"
	>
		Create an event
	</a>

	<div class="border-line mt-8 flex items-center gap-2 rounded-2xl border bg-white p-4">
		{#await probe}
			<span class="size-2.5 shrink-0 rounded-full bg-[#a5a099]"></span>
			<code class="font-mono text-[13.5px]">api: checking…</code>
		{:then health}
			<span class="bg-yes size-2.5 shrink-0 rounded-full"></span>
			<code class="font-mono text-[13.5px]">api: {health.status}</code>
			<span class="text-muted text-[12.5px]">{health.env}</span>
		{:catch err}
			<span class="bg-bad size-2.5 shrink-0 rounded-full"></span>
			<code class="font-mono text-[13.5px]">api: unreachable</code>
			<span class="text-muted text-[12.5px]">{err.message}</span>
		{/await}
	</div>
</main>
