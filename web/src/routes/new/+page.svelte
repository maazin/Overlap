<script lang="ts">
	import { goto } from '$app/navigation';
	import { browser } from '$app/environment';
	import { createEvent, APIError } from '$lib/api';
	import { detectTimezone, allTimezones, saveToken } from '$lib/identity';
	import Button from '$lib/ui/Button.svelte';
	import Card from '$lib/ui/Card.svelte';
	import Field from '$lib/ui/Field.svelte';
	import PageHeader from '$lib/ui/PageHeader.svelte';

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
	let organizerName = $state('');
	let windowStart = $state(browser ? todayIn(detectTimezone()) : '');
	let windowEnd = $state(browser ? addDays(todayIn(detectTimezone()), 6) : '');
	let dayStart = $state('09:00');
	let dayEnd = $state('17:00');
	let slotMinutes = $state(30);

	let busy = $state(false);
	let error = $state('');

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';

		try {
			const res = await createEvent({
				title: title.trim(),
				timezone,
				window_start: windowStart,
				window_end: windowEnd,
				day_start: dayStart,
				day_end: dayEnd,
				slot_minutes: Number(slotMinutes),
				organizer_name: organizerName.trim() || undefined
			});

			// Saved before navigating so the organizer lands already recognised
			// rather than being asked for their name a second time.
			if (res.organizer_token) saveToken(res.slug, res.organizer_token);
			await goto(`/e/${res.slug}`);
		} catch (err) {
			error = err instanceof APIError ? err.message : String(err);
			busy = false;
		}
	}
</script>

<svelte:head>
	<title>New event</title>
</svelte:head>

<div class="u-column pt-8 pb-16">
	<PageHeader title="What are you scheduling?">
		You get a link to paste into the group chat.
	</PageHeader>

	{#if error}
		<div class="mb-5">
			<Card tone="critical"><p class="m-0 text-subhead">{error}</p></Card>
		</div>
	{/if}

	<form onsubmit={submit}>
		<Card>
			<Field label="Title" bind:value={title} placeholder="Team sync" required />

			<Field
				label="Your name"
				bind:value={organizerName}
				placeholder="Optional"
				autocomplete="name"
				hint="Adding your name joins you as a required attendee."
			/>

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
				<p class="mt-1.5 mb-0 text-footnote u-faint">
					The dates above are read in this zone. Everyone else sees their own.
				</p>
			</div>

			<Button variant="primary" type="submit" disabled={busy}>
				{busy ? 'Creating the link' : 'Create the link'}
			</Button>
		</Card>
	</form>
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
