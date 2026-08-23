<script lang="ts">
	import { page } from '$app/state';
	import { browser } from '$app/environment';
	import {
		getEvent,
		joinEvent,
		putResponses,
		connectICS,
		disconnectCalendar,
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
	import Button from '$lib/ui/Button.svelte';
	import Card from '$lib/ui/Card.svelte';
	import Field from '$lib/ui/Field.svelte';
	import PageHeader from '$lib/ui/PageHeader.svelte';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();
	const slug = page.params.slug!;

	/** Link preview copy, built from the server render so a crawler sees it. */
	const ogTitle = $derived(data.event ? `When can you make ${data.event.title}?` : 'Overlap');
	const ogDescription = $derived(
		data.event
			? `${data.event.slot_minutes} minutes, ${windowLabel(data.event)}. Tap the times that work. No account, about fifteen seconds.`
			: "Everyone's free time, in fifteen seconds."
	);

	function windowLabel(ev: EventView): string {
		const fmt = (d: string) =>
			new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' }).format(
				new Date(`${d}T12:00:00Z`)
			);
		return ev.window_start === ev.window_end
			? fmt(ev.window_start)
			: `${fmt(ev.window_start)} to ${fmt(ev.window_end)}`;
	}

	type Stage = 'loading' | 'name' | 'coarse' | 'fine' | 'done' | 'error';

	let stage = $state<Stage>('loading');
	let event = $state<EventView | null>(null);
	$effect.pre(() => {
		if (!event) event = data.event;
	});
	let error = $state('');
	let busy = $state(false);

	let name = $state('');
	let timezone = $state(browser ? detectTimezone() : 'UTC');
	let showTZPicker = $state(false);
	let token = $state<string | null>(null);

	let calendarURL = $state('');
	let calendarOpen = $state(false);
	let calendarNote = $state('');
	const calendarSource = $derived(event?.you?.calendar_source ?? 'none');

	let busySlots = $state<Record<string, true>>({});
	let coarse = $state<Record<string, Tier>>({});
	let fine = $state<Record<string, Tier>>({});

	const slotInstants = $derived((event?.slots ?? []).map((s) => new Date(s)));
	const days = $derived(event ? groupByDay(slotInstants, timezone) : []);
	const coarseCount = $derived(Object.keys(coarse).length);

	/**
	 * Only the day parts that hold a slot somewhere in the window.
	 *
	 * A nine to five event has no evening at all, and a dead third column costs
	 * a fifth of the screen while reading as something the responder failed to
	 * fill in. Which parts survive depends on the viewer's zone, so this cannot
	 * be settled once on the server.
	 */
	const activeBlocks = $derived(BLOCKS.filter((b) => days.some((d) => d.blocks[b].length > 0)));
	const gridCols = $derived(`3.75rem repeat(${activeBlocks.length}, minmax(0, 1fr))`);

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
	 * Rebuilds both editors from stored answers so reopening the link is an
	 * edit rather than a fresh start.
	 */
	function hydrate(ev: EventView) {
		const nextCoarseState: Record<string, Tier> = {};
		const nextFineState: Record<string, Tier> = {};
		const nextBusy: Record<string, true> = {};

		for (const r of ev.you?.responses ?? []) {
			const instant = new Date(r.slot_start);
			if (r.source === 'calendar') {
				// Held apart from the editors. An inferred tier is a proposal to
				// correct, and folding it in would make it look like an answer
				// the person gave.
				nextBusy[slotKey(instant)] = true;
				continue;
			}
			if (r.source === 'coarse') {
				nextCoarseState[cellKey(cellFor(instant, ev.you!.timezone))] = r.tier;
			} else {
				// Keyed through slotKey, never the raw string. The server emits
				// RFC3339 without milliseconds and toISOString always writes
				// them, so the two forms of the same instant do not match.
				nextFineState[slotKey(instant)] = r.tier;
				const k = cellKey(cellFor(instant, ev.you!.timezone));
				if (!(k in nextCoarseState) && r.tier !== 'no') nextCoarseState[k] = 'ok';
			}
		}

		coarse = nextCoarseState;
		fine = nextFineState;
		busySlots = nextBusy;
	}

	/** The canonical key for a slot. Identity is the instant. */
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

	function busyCount(instants: Date[]): number {
		return instants.filter((i) => busySlots[slotKey(i)]).length;
	}

	async function ensureJoined(): Promise<boolean> {
		if (token) return true;
		if (!name.trim()) {
			error = 'A name is needed so the organizer knows who answered.';
			return false;
		}
		const joined = await joinEvent(slug, { name: name.trim(), timezone });
		token = joined.token;
		saveToken(slug, joined.token);
		return true;
	}

	async function submit() {
		if (!event) return;
		busy = true;
		error = '';

		try {
			if (!(await ensureJoined())) return;

			await putResponses(slug, token!, {
				timezone,
				coarse: Object.entries(coarse).map(([k, tier]) => {
					const [date, block] = k.split('|');
					return { date, block: block as Block, tier };
				}),
				// Only overrides that differ from the inherited value are worth
				// sending. The rest are already implied by the day part.
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

	async function linkCalendar() {
		if (!calendarURL.trim()) return;
		busy = true;
		error = '';
		calendarNote = '';

		try {
			if (!(await ensureJoined())) return;

			const res = await connectICS(slug, token!, calendarURL.trim());
			calendarNote =
				res.slots_blocked === 0
					? 'Nothing in your calendar clashes with these times.'
					: `${res.slots_blocked} time${res.slots_blocked === 1 ? '' : 's'} you are already booked have been ruled out. Tap any of them to override.`;
			calendarOpen = false;

			event = await getEvent(slug, token);
			hydrate(event);
			stage = 'coarse';
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			busy = false;
		}
	}

	async function unlinkCalendar() {
		if (!token) return;
		busy = true;
		error = '';
		try {
			await disconnectCalendar(slug, token);
			calendarNote = '';
			event = await getEvent(slug, token);
			hydrate(event);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
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
	<title>{ogTitle}</title>
	<meta name="description" content={ogDescription} />
	<meta property="og:type" content="website" />
	<meta property="og:site_name" content="Overlap" />
	<meta property="og:title" content={ogTitle} />
	<meta property="og:description" content={ogDescription} />
	<meta name="twitter:card" content="summary" />
	<meta name="twitter:title" content={ogTitle} />
	<meta name="twitter:description" content={ogDescription} />
</svelte:head>

<div class="u-column pt-8 pb-52">
	{#if stage === 'loading'}
		<p class="py-20 text-center text-subhead u-faint">Loading</p>
	{:else if stage === 'error'}
		<Card tone="critical">
			<h2 class="m-0 text-heading font-semibold">This link did not open</h2>
			<p class="mt-2 mb-0 text-subhead">{error}</p>
		</Card>
	{:else if event}
		<PageHeader title={event.title}>
			{event.slot_minutes} minutes, shown in
			<button class="tzbtn" onclick={() => (showTZPicker = !showTZPicker)}>
				{timezone.replace(/_/g, ' ')}
			</button>
		</PageHeader>

		{#if showTZPicker}
			<div class="mb-5">
				<label class="mb-1.5 block text-footnote font-semibold u-muted" for="tz">Timezone</label>
				<select
					id="tz"
					class="min-h-touch w-full rounded-[var(--radius-control)] border px-3 text-body"
					bind:value={timezone}
					onchange={() => (showTZPicker = false)}
				>
					{#each allTimezones() as z (z)}
						<option value={z}>{z.replace(/_/g, ' ')}</option>
					{/each}
				</select>
			</div>
		{/if}

		{#if error}
			<div class="mb-5">
				<Card tone="critical">
					<p class="m-0 text-subhead">{error}</p>
				</Card>
			</div>
		{/if}

		<!-- Name -->
		{#if stage === 'name'}
			<div class="mb-4">
				<Card>
					<h2 class="m-0 mb-1 text-heading font-semibold">Who is answering?</h2>
					<p class="mt-0 mb-4 text-subhead u-muted">
						No account and no email. Your answer stays on this device so you can come back and
						change it.
					</p>
					<Field
						label="Your name"
						bind:value={name}
						placeholder="First name is plenty"
						autocomplete="name"
						enterkeyhint="go"
						onkeydown={(e) => e.key === 'Enter' && startAnswering()}
					/>
					<Button variant="primary" onclick={startAnswering}>Start</Button>
				</Card>
			</div>
		{/if}

		<!-- Calendar -->
		{#if stage === 'name' || stage === 'coarse'}
			<div class="mb-4">
				{#if calendarSource === 'none'}
					<Card>
						<h2 class="m-0 mb-1 text-heading font-semibold">Skip the typing</h2>
						<p class="mt-0 mb-4 text-subhead u-muted">
							Paste a calendar link and the hours you are already booked get ruled out for you.
							Overlap reads your free and busy times, never what any of it is for.
						</p>

						{#if calendarOpen}
							<Field
								label="Calendar address"
								bind:value={calendarURL}
								placeholder="webcal:// or https://"
								inputmode="url"
								autocomplete="off"
								hint="Apple Calendar and Outlook both put a secret iCal address in their share menu. In Google it sits under Settings, then Integrate calendar."
							/>
							<Button variant="primary" onclick={linkCalendar} disabled={busy || !calendarURL.trim()}>
								{busy ? 'Reading your calendar' : 'Use this calendar'}
							</Button>
						{:else}
							<Button onclick={() => (calendarOpen = true)}>Connect a calendar</Button>
						{/if}
					</Card>
				{:else}
					<Card tone="positive">
						<p class="m-0 mb-2 text-caption font-semibold tracking-[0.14em] uppercase opacity-70">
							Calendar connected
						</p>
						<p class="m-0 text-subhead opacity-90">
							{calendarNote || 'Times you are already booked are marked below and ruled out.'}
						</p>
						<div class="mt-4">
							<Button variant="quiet" onclick={unlinkCalendar} disabled={busy}>
								Disconnect and clear
							</Button>
						</div>
					</Card>
				{/if}
			</div>
		{/if}

		<!-- Coarse grid -->
		{#if stage === 'coarse'}
			<h2 class="m-0 mb-1 text-heading font-semibold">Tap whatever works</h2>
			<p class="mt-0 mb-5 text-subhead u-muted">
				Once for yes, twice for if needed. Anything you leave alone counts as no.
			</p>

			<div class="mb-2 grid gap-2" style="grid-template-columns: {gridCols}">
				<span></span>
				{#each activeBlocks as b (b)}
					<span class="text-center text-caption font-semibold tracking-[0.1em] uppercase u-faint">
						{BLOCK_LABELS[b]}
					</span>
				{/each}
			</div>

			{#each days as day (day.date)}
				<div class="mb-2 grid items-stretch gap-2" style="grid-template-columns: {gridCols}">
					<div class="flex flex-col justify-center">
						<b class="text-subhead font-semibold">{day.weekday}</b>
						<span class="text-caption u-faint">{day.dayLabel}</span>
					</div>

					{#each activeBlocks as b (b)}
						{@const slotsHere = day.blocks[b]}
						{@const tier = coarse[cellKey({ date: day.date, block: b })]}
						{@const nBusy = busyCount(slotsHere)}
						{@const allBusy = slotsHere.length > 0 && nBusy === slotsHere.length}
						<button
							class="cell {slotsHere.length === 0
								? 'empty'
								: tier === 'ok'
									? 'yes'
									: tier === 'if_needed'
										? 'maybe'
										: allBusy
											? 'busy'
											: 'open'}"
							disabled={slotsHere.length === 0}
							aria-label="{day.weekday} {day.dayLabel} {BLOCK_LABELS[b]}{allBusy
								? ', busy in your calendar'
								: ''}"
							aria-pressed={!!tier}
							onclick={() => tapCell(day.date, b)}
						>
							{tier ? TIER_LABELS[tier] : allBusy ? 'Busy' : nBusy > 0 ? `${nBusy} busy` : ''}
						</button>
					{/each}
				</div>
			{/each}

			<div class="mt-4 flex flex-wrap gap-x-5 gap-y-2 text-caption u-muted">
				<span class="flex items-center gap-2"><i class="key yes"></i>Works</span>
				<span class="flex items-center gap-2"><i class="key maybe"></i>If needed</span>
				{#if calendarSource !== 'none'}
					<span class="flex items-center gap-2"><i class="key busy"></i>Busy in your calendar</span>
				{/if}
			</div>
		{/if}

		<!-- Fine pass -->
		{#if stage === 'fine'}
			<h2 class="m-0 mb-1 text-heading font-semibold">Anything to narrow down?</h2>
			<p class="mt-0 mb-5 text-subhead u-muted">
				Optional. Tap a time to change it or rule it out, or submit as it stands.
			</p>

			<Card>
				{#if chosenCells.length === 0}
					<p class="m-0 text-subhead u-muted">
						You did not mark anything as workable, so there is nothing to narrow. Submitting says
						none of these times work for you.
					</p>
				{:else}
					{#each chosenCells as { day, block } (day.date + block)}
						<div class="block-group">
							<h3 class="m-0 mb-3 text-subhead font-semibold">
								{day.weekday}
								{day.dayLabel}, {BLOCK_LABELS[block]}
							</h3>
							<div class="flex flex-wrap gap-2">
								{#each day.blocks[block] as instant (instant.toISOString())}
									{@const cell = { date: day.date, block }}
									{@const tier = tierFor(instant, cell)}
									<button
										class="chip {tier === 'preferred'
											? 'best'
											: tier === 'ok'
												? 'yes'
												: tier === 'if_needed'
													? 'maybe'
													: 'no'}"
										aria-label="{slotLabel(instant, timezone)}, {TIER_LABELS[tier]}"
										onclick={() => tapSlot(instant, coarse[cellKey(cell)] ?? 'no')}
									>
										{slotLabel(instant, timezone)}
									</button>
								{/each}
							</div>
						</div>
					{/each}
				{/if}
			</Card>
		{/if}

		<!-- Done -->
		{#if stage === 'done'}
			<div class="mb-4">
				<Card tone="positive">
					<p class="m-0 mb-2 text-caption font-semibold tracking-[0.14em] uppercase opacity-70">
						Answer saved
					</p>
					<p class="m-0 text-subhead opacity-90">
						{event.participants.filter((p) => p.responded).length} of {event.participants.length}
						have answered. Reopen this link any time to change yours.
					</p>
				</Card>
			</div>

			<div class="mb-4">
				<Card>
					<h2 class="m-0 mb-3 text-heading font-semibold">Who is in</h2>
					{#each event.participants as p (p.id)}
						<div class="roster">
							<span class="text-subhead">
								{p.name}
								{#if p.role === 'required'}<span class="ml-1 text-caption u-faint">required</span>{/if}
							</span>
							<span class="text-footnote" style={p.responded ? 'color: var(--positive)' : ''}>
								{p.responded ? 'answered' : 'waiting'}
							</span>
						</div>
					{/each}
				</Card>
			</div>

			<div class="mb-3">
				<Button variant="primary" href="/e/{slug}/results">See what works so far</Button>
			</div>
			<Button onclick={() => (stage = 'coarse')}>Change my answer</Button>
		{/if}
	{/if}
</div>

<!-- The action stays pinned so it is always within thumb reach, which is most
     of what makes this quick on a phone. -->
{#if stage === 'coarse' || stage === 'fine'}
	<div class="actionbar">
		<div class="u-column">
			<p class="m-0 mb-3 text-center text-footnote u-muted">
				{#if stage === 'coarse'}
					{coarseCount === 0
						? 'Tap the times that work for you'
						: `${coarseCount} selected`}
				{:else}
					Optional, you can submit as it stands
				{/if}
			</p>
			{#if stage === 'coarse'}
				<Button variant="primary" onclick={() => (stage = 'fine')} disabled={busy}>
					{coarseCount === 0 ? 'Nothing works for me' : 'Continue'}
				</Button>
			{:else}
				<Button variant="primary" onclick={submit} disabled={busy}>
					{busy ? 'Saving' : 'Submit'}
				</Button>
				<div class="mt-2">
					<Button variant="quiet" onclick={() => (stage = 'coarse')} disabled={busy}>Back</Button>
				</div>
			{/if}
		</div>
	</div>
{/if}

<style>
	.tzbtn {
		color: var(--ink);
		text-decoration: underline;
		text-underline-offset: 0.2em;
		font: inherit;
		background: none;
		border: 0;
		padding: 0;
		cursor: pointer;
	}

	select {
		background: var(--surface);
		border-color: var(--hairline-strong);
		color: var(--ink);
		font-family: inherit;
	}

	/*
	  Grid cells clear the 44px minimum touch target with room to spare. The
	  state is written in the cell as well as shown in its colour, so the grid
	  is still readable without colour vision.
	*/
	.cell {
		min-height: 3.5rem;
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 0.25rem;
		border: 1px solid var(--hairline-strong);
		border-radius: var(--radius-control);
		background: var(--surface);
		color: var(--ink-faint);
		font: inherit;
		font-size: var(--text-caption);
		font-weight: 600;
		cursor: pointer;
		transition: background-color 0.12s, border-color 0.12s, color 0.12s;
	}

	.cell.yes {
		background: var(--positive-deep);
		border-color: var(--positive-deep);
		color: var(--on-deep);
	}

	.cell.maybe {
		background: var(--surface);
		border-color: var(--caution);
		color: var(--caution);
		box-shadow: inset 0 0 0 1px var(--caution);
	}

	.cell.busy {
		border-style: dashed;
		color: var(--ink-faint);
		background: var(--raised);
	}

	.cell.empty {
		background: transparent;
		border-color: var(--hairline);
		cursor: default;
		color: transparent;
	}

	.key {
		width: 0.75rem;
		height: 0.75rem;
		border: 1px solid var(--hairline-strong);
		flex: none;
	}
	.key.yes { background: var(--positive-deep); border-color: var(--positive-deep); }
	.key.maybe { background: var(--surface); border-color: var(--caution); }
	.key.busy { background: var(--raised); border-style: dashed; }

	.block-group + .block-group {
		margin-top: 1.25rem;
		padding-top: 1.25rem;
		border-top: 1px solid var(--hairline);
	}

	.chip {
		min-height: var(--spacing-touch);
		display: flex;
		align-items: center;
		padding: 0 0.875rem;
		border: 1px solid var(--hairline-strong);
		border-radius: var(--radius-control);
		background: var(--surface);
		color: var(--ink);
		font: inherit;
		font-size: var(--text-footnote);
		font-weight: 600;
		font-variant-numeric: tabular-nums;
		cursor: pointer;
	}

	.chip.best {
		background: var(--positive-deep);
		border-color: var(--positive-deep);
		color: var(--on-deep);
	}
	.chip.yes {
		border-color: var(--positive);
		color: var(--positive);
		box-shadow: inset 0 0 0 1px var(--positive);
	}
	.chip.maybe {
		border-color: var(--caution);
		color: var(--caution);
	}
	.chip.no {
		background: var(--raised);
		color: var(--ink-faint);
		text-decoration: line-through;
	}

	.roster {
		display: flex;
		justify-content: space-between;
		align-items: baseline;
		gap: 0.75rem;
		padding: 0.625rem 0;
		border-bottom: 1px solid var(--hairline);
	}
	.roster:last-child { border-bottom: 0; }

	.actionbar {
		position: fixed;
		inset-inline: 0;
		bottom: 0;
		z-index: 40;
		border-top: 1px solid var(--hairline);
		background: var(--canvas);
		padding-top: 1rem;
		/* Clears the home indicator on a phone. */
		padding-bottom: max(1.25rem, env(safe-area-inset-bottom));
	}
</style>
