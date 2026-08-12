<script lang="ts">
	import { page } from '$app/state';
	import { browser } from '$app/environment';
	import {
		getEvent,
		joinEvent,
		putResponses,
		APIError,
		type EventView,
		type Tier,
		type Block
	} from '$lib/api';
	import {
		groupByDay,
		cellKey,
		cellFor,
		slotLabel,
		nextCoarse,
		nextFine,
		BLOCKS,
		BLOCK_LABELS,
		TIER_LABELS,
		type Cell
	} from '$lib/dayparts';
	import { loadToken, saveToken, detectTimezone, allTimezones } from '$lib/identity';

	const slug = page.params.slug!;

	type Stage = 'loading' | 'name' | 'coarse' | 'fine' | 'done' | 'error';

	let stage = $state<Stage>('loading');
	let event = $state<EventView | null>(null);
	let error = $state('');
	let busy = $state(false);

	let name = $state('');
	let timezone = $state(browser ? detectTimezone() : 'UTC');
	let showTZPicker = $state(false);
	let token = $state<string | null>(null);

	/** Coarse selections, keyed by `date|block`. Absent means untapped, i.e. no. */
	let coarse = $state<Record<string, Tier>>({});
	/** Fine overrides, keyed by slot ISO string. */
	let fine = $state<Record<string, Tier>>({});

	const slotInstants = $derived((event?.slots ?? []).map((s) => new Date(s)));
	const days = $derived(event ? groupByDay(slotInstants, timezone) : []);
	const coarseCount = $derived(Object.keys(coarse).length);

	/**
	 * Only the blocks that hold a slot somewhere in the window.
	 *
	 * A 9-to-5 event has no evening at all, and rendering a dead third column
	 * costs a fifth of the screen and reads as something the responder failed to
	 * tap. Which blocks survive depends on the viewer's zone, so this cannot be
	 * decided once on the server.
	 */
	const activeBlocks = $derived(
		BLOCKS.filter((b) => days.some((d) => d.blocks[b].length > 0))
	);
	const gridCols = $derived(`58px repeat(${activeBlocks.length}, minmax(0, 1fr))`);

	/** Blocks the responder marked as workable; the fine pass is scoped to these. */
	const chosenCells = $derived(
		days.flatMap((d) =>
			BLOCKS.filter((b) => coarse[cellKey({ date: d.date, block: b })] && d.blocks[b].length > 0).map(
				(b) => ({ day: d, block: b })
			)
		)
	);

	async function load() {
		try {
			token = loadToken(slug);
			const ev = await getEvent(slug, token);
			event = ev;

			if (ev.you) {
				name = ev.you.name;
				timezone = ev.you.timezone;
				hydrate(ev);
				stage = 'coarse';
			} else {
				token = null;
				stage = 'name';
			}
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
			stage = 'error';
		}
	}

	/**
	 * Rebuilds the two editors from stored responses so reopening the link is an
	 * edit rather than a fresh start. Coarse-sourced tiers repopulate the grid;
	 * manual ones repopulate the fine overrides, which is exactly the split the
	 * server recorded in `source`.
	 */
	function hydrate(ev: EventView) {
		const nextCoarseState: Record<string, Tier> = {};
		const nextFineState: Record<string, Tier> = {};

		for (const r of ev.you?.responses ?? []) {
			const instant = new Date(r.slot_start);
			if (r.source === 'coarse') {
				nextCoarseState[cellKey(cellFor(instant, ev.you!.timezone))] = r.tier;
			} else {
				// Keyed through slotKey, not the raw string. The server emits
				// RFC3339 without milliseconds and toISOString always writes
				// them, so keying on r.slot_start silently fails to match and
				// every fine override is lost on reload.
				nextFineState[slotKey(instant)] = r.tier;
				// A manual tier inside an untouched block still implies the block
				// was chosen, otherwise the fine pass would have nothing to show.
				const k = cellKey(cellFor(instant, ev.you!.timezone));
				if (!(k in nextCoarseState) && r.tier !== 'no') nextCoarseState[k] = 'ok';
			}
		}

		coarse = nextCoarseState;
		fine = nextFineState;
	}

	/**
	 * The canonical key for a slot instant.
	 *
	 * Every map keyed by slot must go through this. Slot identity is the
	 * instant, and two renderings of the same instant must not produce two
	 * keys.
	 */
	function slotKey(instant: Date): string {
		return instant.toISOString();
	}

	function tapCell(date: string, block: Block) {
		const k = cellKey({ date, block });
		const next = nextCoarse(coarse[k]);
		const updated = { ...coarse };
		if (next) updated[k] = next;
		else delete updated[k];
		coarse = updated;
	}

	function tapSlot(instant: Date, inherited: Tier) {
		const k = slotKey(instant);
		fine = { ...fine, [k]: nextFine(fine[k] ?? inherited) };
	}

	function tierFor(instant: Date, cell: Cell): Tier {
		return fine[slotKey(instant)] ?? coarse[cellKey(cell)] ?? 'no';
	}

	async function submit() {
		if (!event) return;
		busy = true;
		error = '';

		try {
			if (!token) {
				const joined = await joinEvent(slug, { name: name.trim(), timezone });
				token = joined.token;
				saveToken(slug, joined.token);
			}

			await putResponses(slug, token, {
				timezone,
				coarse: Object.entries(coarse).map(([k, tier]) => {
					const [date, block] = k.split('|');
					return { date, block: block as Block, tier };
				}),
				// Only overrides that differ from the inherited block value are worth
				// sending; the rest are already implied by the coarse selection.
				slots: Object.entries(fine)
					.filter(([iso, tier]) => {
						const cell = cellFor(new Date(iso), timezone);
						return (coarse[cellKey(cell)] ?? 'no') !== tier;
					})
					.map(([iso, tier]) => ({ slot_start: iso, tier }))
			});

			event = await getEvent(slug, token);
			stage = 'done';
		} catch (e) {
			error = e instanceof APIError ? e.message : String(e);
		} finally {
			busy = false;
		}
	}

	function startAnswering() {
		if (!name.trim()) {
			error = 'A name is needed so the organizer knows who answered.';
			return;
		}
		error = '';
		stage = 'coarse';
	}

	if (browser) load();
</script>

<svelte:head>
	<title>{event ? event.title : 'Overlap'}</title>
</svelte:head>

<div class="mx-auto max-w-[480px] px-4 pt-5 pb-32">
	{#if stage === 'loading'}
		<p class="text-muted py-16 text-center text-sm">Loading…</p>
	{:else if stage === 'error'}
		<div class="border-line rounded-2xl border bg-white p-4">
			<h2 class="mb-1 text-[15px] font-semibold">This link did not open</h2>
			<p class="text-muted text-[13.5px]">{error}</p>
		</div>
	{:else if event}
		<header class="mb-4">
			<p class="text-muted text-xs font-semibold tracking-[0.09em] uppercase">Overlap</p>
			<h1 class="mt-1.5 mb-1 text-[23px] leading-tight font-semibold tracking-tight">
				{event.title}
			</h1>
			<p class="text-muted m-0 text-sm">
				{event.slot_minutes} min · shown in
				<button
					class="underline underline-offset-2"
					onclick={() => (showTZPicker = !showTZPicker)}
				>
					{timezone.replace(/_/g, ' ')}
				</button>
			</p>

			{#if showTZPicker}
				<select
					class="border-line mt-2 w-full rounded-lg border bg-white p-2 text-sm"
					bind:value={timezone}
					onchange={() => (showTZPicker = false)}
				>
					{#each allTimezones() as z (z)}
						<option value={z}>{z.replace(/_/g, ' ')}</option>
					{/each}
				</select>
			{/if}
		</header>

		{#if error}
			<p class="text-bad border-bad/30 mb-3 rounded-xl border bg-white p-3 text-[13px]">{error}</p>
		{/if}

		<!-- Stage: name -->
		{#if stage === 'name'}
			<div class="border-line rounded-2xl border bg-white p-4">
				<h2 class="mb-1 text-[15px] font-semibold">Who's answering?</h2>
				<p class="text-muted m-0 text-[13.5px]">
					No account, no email. Your answer stays on this device so you can edit it later.
				</p>
				<input
					class="border-line mt-3 w-full rounded-xl border p-3 text-base"
					placeholder="Your name"
					bind:value={name}
					onkeydown={(e) => e.key === 'Enter' && startAnswering()}
					autocomplete="name"
					enterkeyhint="go"
				/>
				<button
					class="bg-ink mt-2 w-full rounded-xl p-3.5 text-[15px] font-semibold text-white"
					onclick={startAnswering}
				>
					Start
				</button>
			</div>

			<!-- Stage: coarse -->
		{:else if stage === 'coarse'}
			<div class="border-line mb-3.5 rounded-2xl border bg-white p-4">
				<h2 class="mb-1 text-[15px] font-semibold">Tap whatever works</h2>
				<p class="text-muted m-0 text-[13.5px]">
					Once for yes, twice for if-needed. Anything you don't tap counts as no.
				</p>
			</div>

			<div class="mb-1.5 grid gap-1.5" style="grid-template-columns: {gridCols}">
				<span></span>
				{#each activeBlocks as b (b)}
					<span
						class="text-muted text-center text-[10.5px] font-semibold tracking-wider uppercase"
					>
						{BLOCK_LABELS[b]}
					</span>
				{/each}
			</div>

			{#each days as day (day.date)}
				<div class="mb-1.5 grid items-stretch gap-1.5" style="grid-template-columns: {gridCols}">
					<div class="flex flex-col justify-center leading-tight">
						<b class="text-[13px] font-semibold">{day.weekday}</b>
						<span class="text-muted text-[11px]">{day.dayLabel}</span>
					</div>

					{#each activeBlocks as b (b)}
						{@const slotsHere = day.blocks[b]}
						{@const tier = coarse[cellKey({ date: day.date, block: b })]}
						<button
							class="flex min-h-[52px] items-center justify-center rounded-[10px] border p-0.5 text-[11.5px] font-semibold transition-colors
								{slotsHere.length === 0
								? 'border-line cursor-default bg-transparent text-transparent'
								: tier === 'ok'
									? 'border-yes bg-yes-bg text-yes'
									: tier === 'if_needed'
										? 'border-maybe bg-maybe-bg text-maybe'
										: 'border-line bg-white text-[#c4bfb7]'}"
							disabled={slotsHere.length === 0}
							aria-label="{day.weekday} {day.dayLabel} {BLOCK_LABELS[b]}"
							aria-pressed={!!tier}
							onclick={() => tapCell(day.date, b)}
						>
							{tier ? TIER_LABELS[tier] : ''}
						</button>
					{/each}
				</div>
			{/each}

			<div class="text-muted mx-0.5 mt-2.5 flex flex-wrap gap-3 text-[11.5px]">
				<span><i class="border-yes bg-yes-bg mr-1.5 inline-block size-2.5 rounded-[3px] border align-[-1px]"></i>Works</span>
				<span><i class="border-maybe bg-maybe-bg mr-1.5 inline-block size-2.5 rounded-[3px] border align-[-1px]"></i>If needed</span>
			</div>

			<!-- Stage: fine -->
		{:else if stage === 'fine'}
			<div class="border-line mb-3.5 rounded-2xl border bg-white p-4">
				<h2 class="mb-1 text-[15px] font-semibold">Anything to narrow down?</h2>
				<p class="text-muted m-0 text-[13.5px]">
					Optional. Tap a time to change it or rule it out. Otherwise just submit.
				</p>
			</div>

			<div class="border-line rounded-2xl border bg-white p-4">
				{#if chosenCells.length === 0}
					<p class="text-muted m-0 text-[13.5px]">
						You didn't mark anything as workable, so there's nothing to narrow. Submitting says
						none of these times work.
					</p>
				{:else}
					{#each chosenCells as { day, block } (day.date + block)}
						<div class="border-line mt-3 border-t pt-3 first:mt-0 first:border-t-0 first:pt-0">
							<div class="mb-2 text-[13px] font-semibold">
								{day.weekday}
								{day.dayLabel} · {BLOCK_LABELS[block]}
							</div>
							<div class="flex flex-wrap gap-1.5">
								{#each day.blocks[block] as instant (instant.toISOString())}
									{@const cell = { date: day.date, block }}
									{@const tier = tierFor(instant, cell)}
									<button
										class="flex min-h-[38px] items-center rounded-[9px] border px-2.5 py-2 text-[12.5px] font-semibold tabular-nums
											{tier === 'preferred'
											? 'border-yes bg-yes text-white'
											: tier === 'ok'
												? 'border-yes bg-yes-bg text-yes'
												: tier === 'if_needed'
													? 'border-maybe bg-maybe-bg text-maybe'
													: 'border-line bg-[#f4f2ef] text-[#b8b3ab] line-through'}"
										aria-label="{slotLabel(instant, timezone)} — {TIER_LABELS[tier]}"
										onclick={() => tapSlot(instant, coarse[cellKey(cell)] ?? 'no')}
									>
										{slotLabel(instant, timezone)}
									</button>
								{/each}
							</div>
						</div>
					{/each}
				{/if}
			</div>

			<!-- Stage: done -->
		{:else if stage === 'done'}
			<div class="border-yes bg-yes-bg rounded-2xl border p-4">
				<h2 class="text-yes mb-1 text-[15px] font-semibold">Answer saved</h2>
				<p class="text-yes m-0 text-[13.5px]">
					{event.participants.filter((p) => p.responded).length} of {event.participants.length} have
					answered. You can reopen this link any time to change yours.
				</p>
			</div>

			<div class="border-line mt-3.5 rounded-2xl border bg-white p-4">
				<h2 class="mb-2 text-[15px] font-semibold">Who's in</h2>
				{#each event.participants as p (p.id)}
					<div class="border-line flex justify-between border-b py-1.5 text-[13.5px] last:border-b-0">
						<span>
							{p.name}
							{#if p.role === 'required'}<span class="text-muted text-[11px]">· required</span>{/if}
						</span>
						<span class={p.responded ? 'text-yes' : 'text-muted'}>
							{p.responded ? 'answered' : 'waiting'}
						</span>
					</div>
				{/each}
			</div>

			<a
				class="bg-ink mt-3.5 block w-full rounded-xl p-3.5 text-center text-[15px] font-semibold text-white"
				href="/e/{slug}/results"
			>
				See what works so far
			</a>

			<button
				class="border-line mt-2 w-full rounded-xl border bg-white p-3.5 text-[15px] font-semibold"
				onclick={() => (stage = 'coarse')}
			>
				Change my answer
			</button>
		{/if}
	{/if}
</div>

<!-- Sticky action bar. Kept out of the scroll area so the primary action is
	 always within thumb reach, which is most of what makes this fast on a phone. -->
{#if stage === 'coarse' || stage === 'fine'}
	<div
		class="border-line fixed inset-x-0 bottom-0 z-40 border-t bg-[#faf9f7]/95 px-4 pt-3 pb-[max(1.125rem,env(safe-area-inset-bottom))] backdrop-blur"
	>
		<div class="mx-auto max-w-[480px]">
			<p class="text-muted mb-2 text-center text-[12.5px]">
				{#if stage === 'coarse'}
					{coarseCount === 0
						? 'Tap the blocks that work for you'
						: `${coarseCount} block${coarseCount === 1 ? '' : 's'} selected`}
				{:else}
					Optional — you can submit as is
				{/if}
			</p>
			{#if stage === 'coarse'}
				<button
					class="bg-ink w-full rounded-xl p-3.5 text-[15px] font-semibold text-white disabled:opacity-60"
					onclick={() => (stage = 'fine')}
					disabled={busy}
				>
					{coarseCount === 0 ? 'Nothing works for me' : 'Continue'}
				</button>
			{:else}
				<button
					class="bg-ink w-full rounded-xl p-3.5 text-[15px] font-semibold text-white disabled:opacity-60"
					onclick={submit}
					disabled={busy}
				>
					{busy ? 'Saving…' : 'Submit'}
				</button>
				<button
					class="text-muted mt-2 w-full p-2 text-[13.5px]"
					onclick={() => (stage = 'coarse')}
					disabled={busy}
				>
					Back
				</button>
			{/if}
		</div>
	</div>
{/if}
