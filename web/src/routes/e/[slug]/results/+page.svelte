<script lang="ts">
	import { page } from '$app/state';
	import { browser } from '$app/environment';
	import { goto } from '$app/navigation';
	import {
		getEvent,
		solve,
		decide,
		reopen,
		icsURL,
		graduate,
		APIError,
		type EventView,
		type SolveView,
		type RankedSlot
	} from '$lib/api';
	import { groupByDay, slotLabel, BLOCKS } from '$lib/dayparts';
	import { loadToken, detectTimezone, saveGroupToken } from '$lib/identity';
	import { subscribe } from '$lib/live';
	import Button from '$lib/ui/Button.svelte';
	import Card from '$lib/ui/Card.svelte';
	import Field from '$lib/ui/Field.svelte';
	import Banner from '$lib/ui/Banner.svelte';
	import PageHeader from '$lib/ui/PageHeader.svelte';

	const slug = page.params.slug!;

	let event = $state<EventView | null>(null);
	let result = $state<SolveView | null>(null);
	let error = $state('');
	let busy = $state(false);
	let confirming = $state<string | null>(null);

	let graduateOpen = $state(false);
	let groupName = $state('');
	let graduating = $state(false);

	const token = browser ? loadToken(slug) : null;
	const timezone = $derived(event?.you?.timezone ?? (browser ? detectTimezone() : 'UTC'));
	const isOrganizer = $derived(event?.you?.is_organizer ?? false);
	const decided = $derived(result?.decided_slot_start ? new Date(result.decided_slot_start) : null);

	/** The top three live options. A ruled out slot is never offered. */
	const top = $derived((result?.ranked ?? []).filter((r) => !r.eliminated).slice(0, 3));

	const bySlot = $derived(
		new Map((result?.ranked ?? []).map((r) => [new Date(r.slot_start).getTime(), r]))
	);

	const days = $derived(
		event ? groupByDay(event.slots.map((s) => new Date(s)), timezone) : []
	);
	const activeBlocks = $derived(BLOCKS.filter((b) => days.some((d) => d.blocks[b].length > 0)));

	const maxCoverage = $derived(
		Math.max(1, ...(result?.ranked ?? []).filter((r) => !r.eliminated).map((r) => r.coverage))
	);

	const outstanding = $derived(
		(event?.participants ?? []).filter((p) => !p.responded).map((p) => p.name)
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

	/**
	 * What the verdict says, taken from the server's own classification.
	 *
	 * Naming who is holding things up, or saying plainly that nobody is, is the
	 * part a plain poll cannot do.
	 */
	const verdict = $derived.by(() => {
		const d = result?.dominance;
		if (!d || decided) return null;

		const leader = d.leader ? longLabel(new Date(d.leader)) : 'The leading time';
		const join = (xs: string[] | undefined) => (xs ?? []).join(' and ');
		const blocking = d.blocking_required ?? [];

		switch (d.verdict) {
			case 'decidable':
				return {
					tone: 'accent' as const,
					label: 'Ready to decide',
					title: `${leader} wins whatever anyone else says.`,
					body: outstanding.length
						? `${join(outstanding)} ${outstanding.length === 1 ? 'has' : 'have'} not replied, and no answer they could give changes the order.`
						: 'Everyone has replied.'
				};
			case 'waiting_on_required':
				return {
					tone: 'caution' as const,
					label: 'Blocked',
					title: `Waiting on ${join(blocking)}.`,
					body: `${blocking.length === 1 ? 'They are' : 'They are'} required and have not replied. Nothing can be settled until they do, because they could still rule out any of these.`
				};
			case 'waiting_on_relevant':
				return {
					tone: 'caution' as const,
					label: 'One answer left',
					title: `Only ${join(d.relevant)} can change the outcome.`,
					body: 'Anyone else outstanding can be left alone. Nothing they say moves the order.'
				};
			case 'tied':
				return {
					tone: 'accent' as const,
					label: 'Ready to decide',
					title: 'It is a tie.',
					body: 'No outstanding reply separates the leading times. Pick whichever suits.'
				};
			case 'no_slots':
				return {
					tone: 'critical' as const,
					label: 'Nothing available',
					title: 'No time works.',
					body: 'Every slot is ruled out by someone required. Widening the window or making somebody optional is the way out.'
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
			if (e instanceof APIError && e.status === 409) confirming = slotStart;
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

	/**
	 * Graduation is offered once a decision has actually landed, never before.
	 * That is the one moment the PRD calls out as the moment someone will
	 * accept the setup cost of a group, because they have just watched
	 * scheduling work rather than being asked to trust that it will.
	 */
	async function doGraduate() {
		if (!token) return;
		graduating = true;
		error = '';
		try {
			const res = await graduate(slug, token, groupName.trim());
			saveGroupToken(res.group_slug, res.token);
			await goto(`/g/${res.group_slug}`);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
			graduating = false;
		}
	}

	/** Heat band for the overview. Ruled out slots read as struck through. */
	function heat(r: RankedSlot | undefined): string {
		if (!r) return 'none';
		if (r.eliminated) return 'dead';
		const share = r.coverage / maxCoverage;
		if (share >= 1) return 'full';
		if (share >= 0.7) return 'high';
		if (share >= 0.4) return 'mid';
		return 'low';
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
		// Live updates. The teardown matters: without it a page somebody has
		// navigated away from keeps a connection open, and the server keeps a
		// subscriber for it.
		$effect(() => subscribe(slug, load));
	}
</script>

<svelte:head>
	<title>{event ? `${event.title}, results` : 'Results'}</title>
</svelte:head>

<div class="u-column pt-8 pb-16">
	{#if error && !event}
		<Card tone="critical">
			<h2 class="m-0 text-heading font-semibold">Could not load results</h2>
			<p class="mt-2 mb-0 text-subhead">{error}</p>
		</Card>
	{:else if event && result}
		<PageHeader title={event.title}>
			{result.responded} of {result.total} replied, times in {timezone.replace(/_/g, ' ')}
		</PageHeader>

		{#if error}
			<div class="mb-5">
				<Card tone="critical"><p class="m-0 text-subhead">{error}</p></Card>
			</div>
		{/if}

		{#if decided}
			<div class="mb-4">
				<Card tone="positive">
					<p class="m-0 mb-2 text-caption font-semibold tracking-[0.14em] uppercase opacity-70">
						Locked
					</p>
					<h2 class="u-serif m-0 text-title font-semibold">{longLabel(decided)}</h2>
					<p class="mt-2 mb-0 text-subhead opacity-90">
						{bySlot.get(decided.getTime())?.coverage ?? 0} of {result.total} can make it.
					</p>
					<div class="mt-5">
						<Button variant="secondary" href={icsURL(slug)} download>Add to calendar</Button>
					</div>
					{#if isOrganizer}
						<div class="mt-2">
							<Button variant="quiet" onclick={unlock} disabled={busy}>Unlock and reopen</Button>
						</div>
					{/if}
				</Card>
			</div>

			{#if isOrganizer && !event.group_slug}
				<div class="mb-4">
					<Card>
						<h2 class="m-0 mb-1 text-heading font-semibold">Schedule with these people again?</h2>
						<p class="mt-0 mb-4 text-subhead u-muted">
							Names, timezones and roles carry over. Next time takes one tap instead of typing
							everything in again.
						</p>
						{#if graduateOpen}
							<Field
								label="Group name"
								bind:value={groupName}
								placeholder={event.title}
								hint="Shown to everyone in the group."
							/>
							<Button variant="primary" onclick={doGraduate} disabled={graduating}>
								{graduating ? 'Creating the group' : 'Make a group'}
							</Button>
						{:else}
							<Button onclick={() => (graduateOpen = true)}>Make a group</Button>
						{/if}
					</Card>
				</div>
			{/if}
		{:else}
			{#if verdict}
				<div class="mb-5">
					<Banner
						tone={verdict.tone}
						label={verdict.label}
						title={verdict.title}
						body={verdict.body}
					/>
				</div>
			{/if}

			{#each top as r, i (r.slot_start)}
				{@const when = new Date(r.slot_start)}
				<div class="mb-3">
					<Card>
						<div class="flex items-baseline justify-between gap-3">
							<span class="text-heading font-semibold tracking-tight">{longLabel(when)}</span>
							<span class="u-tnum shrink-0 text-footnote u-muted">
								{r.coverage} of {r.total}
							</span>
						</div>

						<div class="meter" role="img" aria-label="{r.coverage} of {r.total} can attend">
							<div class="meter-fill" style="width: {(r.coverage / r.total) * 100}%"></div>
						</div>

						<p class="m-0 text-footnote u-muted">
							{#if r.excludes?.length}
								<b class="u-ink font-semibold">{r.excludes.join(', ')}</b>
								cannot make it{r.unknown?.length ? ', ' : ''}
							{/if}
							{#if r.unknown?.length}
								<b class="u-ink font-semibold">{r.unknown.join(', ')}</b>
								{r.unknown.length === 1 ? 'has' : 'have'} not replied
							{/if}
							{#if !r.excludes?.length && !r.unknown?.length}
								Everyone can make it.
							{/if}
							{#if r.unsociable}
								<span style="color: var(--caution)">. Outside working hours for someone.</span>
							{/if}
						</p>

						{#if isOrganizer}
							<div class="mt-4">
								<Button
									variant={i === 0 ? 'primary' : 'secondary'}
									onclick={() => lock(r.slot_start)}
									disabled={busy}
								>
									Lock this time
								</Button>
							</div>
						{/if}
					</Card>
				</div>
			{:else}
				<Card tone="critical">
					<h2 class="m-0 text-heading font-semibold">No time works yet</h2>
					<p class="mt-2 mb-0 text-subhead">
						Every slot is ruled out by someone required. Widening the window or making somebody
						optional is the way out.
					</p>
				</Card>
			{/each}

			{#if confirming}
				<div class="mb-4">
					<Card tone="caution">
						<h2 class="m-0 text-heading font-semibold">Lock it anyway?</h2>
						<p class="mt-2 mb-4 text-subhead">
							Somebody required has ruled this time out. They will not be able to attend.
						</p>
						<div class="flex gap-2">
							<Button variant="caution" full={false} onclick={() => lock(confirming!, true)} disabled={busy}>
								Lock anyway
							</Button>
							<Button full={false} onclick={() => (confirming = null)}>Cancel</Button>
						</div>
					</Card>
				</div>
			{/if}
		{/if}

		<h2 class="mt-10 mb-4 text-heading font-semibold">Every time, by how many can come</h2>
		{#each days as day (day.date)}
			<div class="mb-4">
				<div class="mb-2 text-caption font-semibold tracking-[0.1em] uppercase u-faint">
					{day.weekday}
					{day.dayLabel}
				</div>
				{#each activeBlocks as b (b)}
					{#if day.blocks[b].length}
						<div class="mb-1.5 flex flex-wrap gap-1.5">
							{#each day.blocks[b] as instant (instant.toISOString())}
								{@const r = bySlot.get(instant.getTime())}
								<span
									class="slot h-{heat(r)}"
									title="{r?.coverage ?? 0} of {r?.total ?? 0} can attend"
								>
									{slotLabel(instant, timezone)}
								</span>
							{/each}
						</div>
					{/if}
				{/each}
			</div>
		{/each}

		<div class="mt-6">
			<Button href="/e/{slug}">
				{event.you ? 'Change my answer' : 'Add my availability'}
			</Button>
		</div>
	{:else}
		<p class="py-20 text-center text-subhead u-faint">Loading</p>
	{/if}
</div>

<style>
	.meter {
		height: 0.25rem;
		margin: 0.75rem 0;
		background: var(--raised);
		overflow: hidden;
	}
	.meter-fill {
		height: 100%;
		background: var(--positive);
	}

	/*
	  Coverage is shown by depth of ground rather than by hue, so the overview
	  reads as one material at different weights. A ruled out slot is struck
	  through as well as dimmed, which survives greyscale and colour blindness.
	*/
	.slot {
		display: inline-flex;
		align-items: center;
		padding: 0.4375rem 0.625rem;
		border: 1px solid var(--hairline-strong);
		border-radius: var(--radius-control);
		background: var(--surface);
		color: var(--ink-muted);
		font-size: var(--text-caption);
		font-weight: 600;
		font-variant-numeric: tabular-nums;
	}

	.h-full {
		background: var(--positive-deep);
		border-color: var(--positive-deep);
		color: var(--on-deep);
	}
	.h-high {
		background: var(--surface);
		border-color: var(--positive);
		color: var(--positive);
		box-shadow: inset 0 0 0 1px var(--positive);
	}
	.h-mid {
		border-color: var(--positive-line);
		color: var(--ink-muted);
	}
	.h-low {
		color: var(--ink-faint);
	}
	.h-dead {
		background: var(--raised);
		color: var(--ink-faint);
		text-decoration: line-through;
		border-style: dashed;
	}
</style>
