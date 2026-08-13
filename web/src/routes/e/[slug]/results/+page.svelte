<script lang="ts">
	import { page } from '$app/state';
	import { browser } from '$app/environment';
	import {
		getEvent,
		solve,
		decide,
		reopen,
		icsURL,
		APIError,
		type EventView,
		type SolveView,
		type RankedSlot
	} from '$lib/api';
	import { groupByDay, slotLabel, BLOCKS } from '$lib/dayparts';
	import { loadToken, detectTimezone } from '$lib/identity';
	import { subscribe } from '$lib/live';

	const slug = page.params.slug!;

	let event = $state<EventView | null>(null);
	let result = $state<SolveView | null>(null);
	let error = $state('');
	let busy = $state(false);
	let confirming = $state<string | null>(null);

	const token = browser ? loadToken(slug) : null;
	const timezone = $derived(event?.you?.timezone ?? (browser ? detectTimezone() : 'UTC'));
	const isOrganizer = $derived(event?.you?.is_organizer ?? false);

	const decided = $derived(result?.decided_slot_start ? new Date(result.decided_slot_start) : null);

	/** The top three live slots. Eliminated ones are never offered as a choice. */
	const top = $derived((result?.ranked ?? []).filter((r) => !r.eliminated).slice(0, 3));

	/** Coverage per slot, for the heatmap. */
	const coverageBySlot = $derived(
		new Map((result?.ranked ?? []).map((r) => [new Date(r.slot_start).getTime(), r]))
	);

	const days = $derived(
		event ? groupByDay(event.slots.map((s) => new Date(s)), timezone) : []
	);
	const activeBlocks = $derived(BLOCKS.filter((b) => days.some((d) => d.blocks[b].length > 0)));

	const maxCoverage = $derived(
		Math.max(1, ...(result?.ranked ?? []).filter((r) => !r.eliminated).map((r) => r.coverage))
	);

	async function load() {
		try {
			const [ev, res] = await Promise.all([getEvent(slug, token), solve(slug)]);
			event = ev;
			result = res;
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}

	/** Everyone who has not replied, for the decidable message. */
	const outstanding = $derived(
		(event?.participants ?? []).filter((p) => !p.responded).map((p) => p.name)
	);

	/**
	 * What the banner says, derived from the verdict rather than from raw
	 * counts. The wording is the feature: naming who is blocking, or saying
	 * plainly that nobody is, is what a poll cannot do.
	 */
	const banner = $derived.by(() => {
		const d = result?.dominance;
		if (!d || decided) return null;

		const leaderLabel = d.leader ? longLabel(new Date(d.leader)) : 'The leading time';
		const names = (xs: string[] | undefined) => (xs ?? []).join(' and ');
		const blocking = d.blocking_required ?? [];

		switch (d.verdict) {
			case 'decidable':
				return {
					tone: 'go' as const,
					title: `${leaderLabel} wins whatever anyone else says.`,
					body: outstanding.length
						? `${names(outstanding)} ${outstanding.length === 1 ? 'has' : 'have'} not replied, and no answer they could give changes the order.`
						: 'Everyone has replied.'
				};
			case 'waiting_on_required':
				return {
					tone: 'wait' as const,
					title: `Waiting on ${names(blocking)}.`,
					body: `${blocking.length === 1 ? 'They are required and have' : 'They are required and have'} not replied. Nothing can be settled until they do, because they could still rule out any of these.`
				};
			case 'waiting_on_relevant':
				return {
					tone: 'wait' as const,
					title: `Only ${names(d.relevant)} can change the outcome.`,
					body: 'Anyone else outstanding can be left alone; nothing they say moves the order.'
				};
			case 'tied':
				return {
					tone: 'go' as const,
					title: 'It is a tie.',
					body: 'No outstanding reply separates the leading times. Pick whichever suits.'
				};
			case 'no_slots':
				return {
					tone: 'wait' as const,
					title: 'No time works.',
					body: 'Every slot is ruled out by someone required. Widen the window, or make someone optional.'
				};
			default:
				return null;
		}
	});

	async function lock(slotStart: string, force = false) {
		if (!token) return;
		busy = true;
		error = '';
		try {
			await decide(slug, token, slotStart, force);
			result = await solve(slug);
			confirming = null;
		} catch (e) {
			if (e instanceof APIError && e.status === 409) {
				// The slot is ruled out by someone required. Say who, and make
				// overriding a separate deliberate act rather than a retry.
				confirming = slotStart;
			}
			error = e instanceof Error ? e.message : String(e);
		} finally {
			busy = false;
		}
	}

	async function unlock() {
		if (!token) return;
		busy = true;
		error = '';
		try {
			await reopen(slug, token);
			result = await solve(slug);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			busy = false;
		}
	}

	/** Heat shading. Eliminated slots read as struck out, never as merely cold. */
	function heat(r: RankedSlot | undefined): string {
		if (!r) return 'bg-white border-line';
		if (r.eliminated) return 'bg-[#f4f2ef] border-line text-[#c4bfb7] line-through';
		const share = r.coverage / maxCoverage;
		if (share >= 1) return 'bg-yes text-white border-yes';
		if (share >= 0.75) return 'bg-yes-bg border-yes text-yes';
		if (share >= 0.5) return 'bg-maybe-bg border-maybe text-maybe';
		return 'bg-white border-line text-muted';
	}

	function longLabel(d: Date): string {
		return new Intl.DateTimeFormat(undefined, {
			timeZone: timezone,
			weekday: 'long',
			month: 'short',
			day: 'numeric',
			hour: 'numeric',
			minute: '2-digit'
		}).format(d);
	}

	if (browser) {
		load();
		// Live updates. The teardown matters: without it a navigated-away page
		// keeps a connection open and the server keeps a subscriber for it.
		$effect(() => subscribe(slug, load));
	}
</script>

<svelte:head>
	<title>{event ? `${event.title} — results` : 'Results'}</title>
</svelte:head>

<div class="mx-auto max-w-[480px] px-4 pt-5 pb-16">
	{#if error && !event}
		<div class="border-line rounded-2xl border bg-white p-4">
			<h2 class="mb-1 text-[15px] font-semibold">Could not load results</h2>
			<p class="text-muted text-[13.5px]">{error}</p>
		</div>
	{:else if event && result}
		<header class="mb-4">
			<p class="text-muted text-xs font-semibold tracking-[0.09em] uppercase">Overlap</p>
			<h1 class="mt-1.5 mb-1 text-[23px] leading-tight font-semibold tracking-tight">
				{event.title}
			</h1>
			<p class="text-muted m-0 text-sm">
				{result.responded} of {result.total} replied · times in {timezone.replace(/_/g, ' ')}
			</p>
		</header>

		{#if error}
			<p class="text-bad border-bad/30 mb-3 rounded-xl border bg-white p-3 text-[13px]">{error}</p>
		{/if}

		<!-- Decided state -->
		{#if decided}
			<div class="border-yes bg-yes-bg mb-3.5 rounded-2xl border p-4">
				<p class="text-yes/70 text-[11.5px] font-semibold tracking-wider uppercase">Locked</p>
				<h2 class="text-yes mt-1 mb-1 text-[17px] font-semibold tracking-tight">
					{longLabel(decided)}
				</h2>
				<p class="text-yes m-0 text-[13.5px]">
					{coverageBySlot.get(decided.getTime())?.coverage ?? 0} of {result.total} can make it.
				</p>
				<a
					class="bg-yes mt-3 block w-full rounded-xl p-3.5 text-center text-[15px] font-semibold text-white"
					href={icsURL(slug)}
					download
				>
					Add to calendar
				</a>
				{#if isOrganizer}
					<button
						class="text-yes mt-2 w-full p-2 text-[13.5px] underline underline-offset-2"
						onclick={unlock}
						disabled={busy}
					>
						Unlock and reopen
					</button>
				{/if}
			</div>
		{:else}
			{#if banner}
				<div
					class="mb-3.5 rounded-2xl p-4 {banner.tone === 'go'
						? 'bg-[#0f2f22] text-[#d9f2e6]'
						: 'bg-maybe-bg border border-[#e8d5a8] text-[#5c3d05]'}"
				>
					<h2 class="mb-1 text-[15.5px] font-semibold tracking-tight">{banner.title}</h2>
					<p class="m-0 text-[13px] opacity-85">{banner.body}</p>
				</div>
			{/if}

			<!-- Ranked candidates -->
			<div class="mb-3.5">
				{#each top as r, i (r.slot_start)}
					{@const when = new Date(r.slot_start)}
					<div
						class="mb-2 rounded-2xl border bg-white p-3.5 {i === 0
							? 'border-ink border-2'
							: 'border-line'}"
					>
						<div class="flex items-baseline justify-between gap-2.5">
							<span class="text-[15px] font-semibold tracking-tight">{longLabel(when)}</span>
							<span class="text-muted shrink-0 text-xs tabular-nums">
								{r.coverage} of {r.total}
							</span>
						</div>

						<div class="my-2 h-1.5 overflow-hidden rounded-full bg-[#eeebe6]">
							<div class="bg-yes h-full rounded-full" style="width: {(r.coverage / r.total) * 100}%"></div>
						</div>

						<p class="text-muted m-0 text-[12.5px] leading-relaxed">
							{#if r.excludes?.length}
								<b class="text-ink font-semibold">{r.excludes.join(', ')}</b>
								can't make it{r.unknown?.length ? ' · ' : ''}
							{/if}
							{#if r.unknown?.length}
								<b class="text-ink font-semibold">{r.unknown.join(', ')}</b>
								{r.unknown.length === 1 ? "hasn't" : "haven't"} replied
							{/if}
							{#if !r.excludes?.length && !r.unknown?.length}
								Everyone can make it.
							{/if}
							{#if r.unsociable}
								<span class="text-maybe"> · outside working hours for someone</span>
							{/if}
						</p>

						{#if isOrganizer}
							<button
								class="mt-2.5 w-full rounded-xl p-3 text-[14px] font-semibold {i === 0
									? 'bg-ink text-white'
									: 'border-line border bg-white'}"
								onclick={() => lock(r.slot_start)}
								disabled={busy}
							>
								Lock this time
							</button>
						{/if}
					</div>
				{:else}
					<div class="border-line rounded-2xl border bg-white p-4">
						<h2 class="mb-1 text-[15px] font-semibold">No time works yet</h2>
						<p class="text-muted m-0 text-[13.5px]">
							Every slot is ruled out by someone required. Widening the window or making
							someone optional is the way out.
						</p>
					</div>
				{/each}
			</div>

			{#if confirming}
				<div class="border-maybe bg-maybe-bg mb-3.5 rounded-2xl border p-4">
					<h2 class="text-maybe mb-1 text-[15px] font-semibold">Lock it anyway?</h2>
					<p class="text-maybe m-0 text-[13.5px]">
						Someone required has ruled this time out. They will not be able to attend.
					</p>
					<div class="mt-3 flex gap-2">
						<button
							class="bg-maybe flex-1 rounded-xl p-3 text-[14px] font-semibold text-white"
							onclick={() => lock(confirming!, true)}
							disabled={busy}
						>
							Lock anyway
						</button>
						<button
							class="border-maybe text-maybe flex-1 rounded-xl border bg-white p-3 text-[14px] font-semibold"
							onclick={() => (confirming = null)}
						>
							Cancel
						</button>
					</div>
				</div>
			{/if}
		{/if}

		<!-- Heatmap -->
		<h2 class="mt-6 mb-2 text-[15px] font-semibold">Everything, by how many can come</h2>
		{#each days as day (day.date)}
			<div class="mb-2.5">
				<div class="text-muted mb-1 text-[11.5px] font-semibold tracking-wider uppercase">
					{day.weekday}
					{day.dayLabel}
				</div>
				{#each activeBlocks as b (b)}
					{#if day.blocks[b].length}
						<div class="mb-1 flex flex-wrap gap-1">
							{#each day.blocks[b] as instant (instant.toISOString())}
								{@const r = coverageBySlot.get(instant.getTime())}
								<span
									class="rounded-lg border px-2 py-1.5 text-[11.5px] font-semibold tabular-nums {heat(r)}"
									title="{r?.coverage ?? 0} of {r?.total ?? 0}"
								>
									{slotLabel(instant, timezone)}
								</span>
							{/each}
						</div>
					{/if}
				{/each}
			</div>
		{/each}

		<a
			class="border-line mt-4 block w-full rounded-xl border bg-white p-3.5 text-center text-[15px] font-semibold"
			href="/e/{slug}"
		>
			{event.you ? 'Change my answer' : 'Add my availability'}
		</a>
	{:else}
		<p class="text-muted py-16 text-center text-sm">Loading…</p>
	{/if}
</div>
