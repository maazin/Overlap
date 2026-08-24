<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { browser } from '$app/environment';
	import { createGroupEvent, decide, APIError, type GroupProposal } from '$lib/api';
	import { loadGroupToken, saveToken, detectTimezone, allTimezones } from '$lib/identity';
	import Button from '$lib/ui/Button.svelte';
	import Card from '$lib/ui/Card.svelte';
	import Field from '$lib/ui/Field.svelte';
	import PageHeader from '$lib/ui/PageHeader.svelte';

	const groupSlug = page.params.slug!;

	function todayIn(tz: string): string {
		return new Intl.DateTimeFormat('en-CA', {
			timeZone: tz,
			year: 'numeric',
			month: '2-digit',
			day: '2-digit'
		}).format(new Date());
	}
	function addDays(date: string, n: number): string {
		const d = new Date(`${date}T00:00:00Z`);
		d.setUTCDate(d.getUTCDate() + n);
		return d.toISOString().slice(0, 10);
	}

	let timezone = $state(browser ? detectTimezone() : 'UTC');
	let title = $state('');
	let windowStart = $state(browser ? todayIn(detectTimezone()) : '');
	let windowEnd = $state(browser ? addDays(todayIn(detectTimezone()), 6) : '');
	let dayStart = $state('09:00');
	let dayEnd = $state('17:00');
	let slotMinutes = $state(30);

	let busy = $state(false);
	let error = $state('');

	// After creation: the event exists, and it may already carry a suggestion
	// derived from connected calendars. Confirming books it immediately;
	// opening for responses is the default and stays the more prominent
	// action, because free/busy knows where nobody is committed and nothing
	// about whether the time is one anyone actually wants.
	let created = $state<{ eventSlug: string; organizerToken: string; proposal?: GroupProposal } | null>(
		null
	);
	let confirming = $state(false);

	function proposalLabel(p: GroupProposal): string {
		return new Intl.DateTimeFormat(undefined, {
			timeZone: timezone,
			weekday: 'long',
			month: 'short',
			day: 'numeric',
			hour: 'numeric',
			minute: '2-digit'
		}).format(new Date(p.slot_start));
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		const token = loadGroupToken(groupSlug);
		if (!token) {
			error = 'Open the group link on this device first.';
			return;
		}
		busy = true;
		error = '';

		try {
			const res = await createGroupEvent(groupSlug, token, {
				title: title.trim(),
				timezone,
				window_start: windowStart,
				window_end: windowEnd,
				day_start: dayStart,
				day_end: dayEnd,
				slot_minutes: Number(slotMinutes)
			});
			if (res.organizer_token) saveToken(res.event_slug, res.organizer_token);
			created = {
				eventSlug: res.event_slug,
				organizerToken: res.organizer_token ?? '',
				proposal: res.proposal
			};
		} catch (err) {
			error = err instanceof APIError ? err.message : String(err);
		} finally {
			busy = false;
		}
	}

	async function confirmProposal() {
		if (!created?.proposal || !created.organizerToken) return;
		confirming = true;
		error = '';
		try {
			await decide(created.eventSlug, created.organizerToken, created.proposal.slot_start);
			await goto(`/e/${created.eventSlug}/results`);
		} catch (err) {
			error = err instanceof APIError ? err.message : String(err);
			confirming = false;
		}
	}
</script>

<svelte:head>
	<title>New event</title>
</svelte:head>

<div class="u-column pt-8 pb-16">
	<PageHeader title="What are you scheduling?">
		Names, timezones and roles carry over from the group automatically.
	</PageHeader>

	{#if error}
		<div class="mb-5">
			<Card tone="critical"><p class="m-0 text-subhead">{error}</p></Card>
		</div>
	{/if}

	{#if !created}
		<form onsubmit={submit}>
			<Card>
				<Field label="Title" bind:value={title} placeholder="Team sync" required />

				<div class="grid grid-cols-2 gap-3">
					<Field label="From" bind:value={windowStart} type="date" required />
					<Field label="To" bind:value={windowEnd} type="date" required />
				</div>

				<div class="grid grid-cols-2 gap-3">
					<Field label="Day starts" bind:value={dayStart} type="time" required />
					<Field label="Day ends" bind:value={dayEnd} type="time" required />
				</div>

				<div class="mb-4">
					<label class="mb-1.5 block text-footnote font-semibold u-muted" for="len">
						Meeting length
					</label>
					<select id="len" class="picker" bind:value={slotMinutes}>
						{#each [15, 30, 45, 60, 90, 120] as m (m)}
							<option value={m}>{m} minutes</option>
						{/each}
					</select>
				</div>

				<div class="mb-5">
					<label class="mb-1.5 block text-footnote font-semibold u-muted" for="tz">
						Your timezone
					</label>
					<select id="tz" class="picker" bind:value={timezone}>
						{#each allTimezones() as z (z)}
							<option value={z}>{z.replace(/_/g, ' ')}</option>
						{/each}
					</select>
				</div>

				<Button variant="primary" type="submit" disabled={busy}>
					{busy ? 'Creating' : 'Create the event'}
				</Button>
			</Card>
		</form>
	{:else if created.proposal}
		<Card tone="accent">
			<p class="m-0 mb-2 text-caption font-semibold tracking-[0.14em] uppercase opacity-60">
				Suggested from connected calendars
			</p>
			<p class="m-0 text-heading font-semibold tracking-tight">
				{proposalLabel(created.proposal)}
			</p>
			<p class="mt-2 mb-0 text-subhead opacity-75">
				Free for {created.proposal.considered.join(' and ')}. This is a suggestion, not a booking:
				it knows where nobody is committed, not whether anyone actually wants this time.
			</p>
		</Card>

		<div class="mt-4">
			<Button variant="primary" href="/e/{created.eventSlug}">Open for responses</Button>
		</div>
		<div class="mt-2">
			<Button variant="secondary" onclick={confirmProposal} disabled={confirming}>
				{confirming ? 'Confirming' : 'Confirm this time'}
			</Button>
		</div>
	{:else}
		<Card tone="positive">
			<h2 class="m-0 mb-1 text-heading font-semibold">Event created</h2>
			<p class="mt-0 mb-0 text-subhead opacity-90">
				Not enough connected calendars to suggest a time yet, so this starts as an ordinary poll.
			</p>
		</Card>
		<div class="mt-4">
			<Button variant="primary" href="/e/{created.eventSlug}">Open for responses</Button>
		</div>
	{/if}
</div>

<style>
	.picker {
		width: 100%;
		min-height: var(--spacing-touch);
		padding: 0 0.75rem;
		border: 1px solid var(--hairline-strong);
		border-radius: var(--radius-control);
		background: var(--surface);
		color: var(--ink);
		font-family: inherit;
		font-size: var(--text-body);
	}
	.picker:focus { border-color: var(--accent); }
</style>
