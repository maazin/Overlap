<script lang="ts">
	import { browser } from '$app/environment';
	import { API_URL } from '$lib/api';

	type Health = { status: string; env: string };

	// Runs in the browser rather than in a load function on purpose. The API and
	// the web app are deployed to different hosts, so a same-origin server-side
	// fetch would prove nothing about the cross-origin path the real client uses.
	async function check(): Promise<Health> {
		const res = await fetch(`${API_URL}/api/health`);
		if (!res.ok) throw new Error(`HTTP ${res.status}`);
		return res.json();
	}

	let probe = $state<Promise<Health>>(browser ? check() : new Promise<Health>(() => {}));

	const title = "Overlap — everyone's free time, in fifteen seconds";
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

<main class="mx-auto max-w-[480px] px-4 py-14">
	<!-- First scroll: speed. It is what converts the first visit. -->
	<p class="text-muted text-xs font-semibold tracking-[0.09em] uppercase">Overlap</p>
	<h1 class="mt-1.5 mb-2 text-[27px] leading-[1.15] font-semibold tracking-tight">
		Everyone's free time, in fifteen seconds.
	</h1>
	<p class="text-muted mb-6 text-[15px] leading-relaxed">
		Tap three buttons. No grid to paint, no account, no app. Most people finish before they would
		have finished reading a When2Meet.
	</p>

	<a
		href="/new"
		class="bg-ink block w-full rounded-xl p-4 text-center text-[15px] font-semibold text-white"
	>
		Create an event
	</a>
	<p class="text-muted mt-2 text-center text-[12.5px]">
		You get a link. Paste it into the group chat.
	</p>

	<!-- Second scroll: dominance. This is the part that makes someone tell a
	     friend, but it cannot carry the first impression. -->
	<section class="border-line mt-14 border-t pt-8">
		<h2 class="mb-2 text-[19px] leading-tight font-semibold tracking-tight">
			It tells you when to stop waiting.
		</h2>
		<p class="text-muted mb-5 text-[14px] leading-relaxed">
			Four of six have replied and you have no idea whether the last two matter. Overlap works it
			out, because usually they do not.
		</p>

		<div class="mb-2 rounded-2xl bg-[#0f2f22] p-4 text-[#d9f2e6]">
			<p class="m-0 text-[14.5px] font-semibold tracking-tight">
				Thursday 3pm wins whatever anyone else says.
			</p>
			<p class="m-0 mt-1 text-[12.5px] opacity-85">
				Dev and Cara have not replied, and no answer they could give changes the order.
			</p>
		</div>

		<div class="bg-maybe-bg mb-5 rounded-2xl border border-[#e8d5a8] p-4 text-[#5c3d05]">
			<p class="m-0 text-[14.5px] font-semibold tracking-tight">Waiting on Sam.</p>
			<p class="m-0 mt-1 text-[12.5px] opacity-85">
				Sam is required and has not replied. Nothing can be settled until they do — and nobody
				else needs chasing.
			</p>
		</div>

		<p class="text-muted text-[14px] leading-relaxed">
			It also knows the difference between a time that works and a time someone will resent, keeps
			required attendees from being outvoted by bystanders, and ends where the job actually ends:
			a locked time and a calendar invite.
		</p>
	</section>

	<section class="border-line mt-10 border-t pt-8">
		<h2 class="mb-2 text-[19px] leading-tight font-semibold tracking-tight">
			It never reads your calendar.
		</h2>
		<p class="text-muted text-[14px] leading-relaxed">
			Connecting a calendar asks for free/busy only — the times you are booked, never what for.
			Event titles and details are never fetched, never stored and never logged.
		</p>
	</section>

	<div class="border-line mt-10 flex items-center gap-2 rounded-2xl border bg-white p-4">
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
