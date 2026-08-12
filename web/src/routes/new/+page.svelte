<script lang="ts">
	import { goto } from '$app/navigation';
	import { browser } from '$app/environment';
	import { createEvent, APIError } from '$lib/api';
	import { detectTimezone, allTimezones, saveToken } from '$lib/identity';

	/** Today in the given zone, as YYYY-MM-DD. */
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

			// Stored before navigating so the organizer lands on the response page
			// already recognised, rather than being asked for their name again.
			if (res.organizer_token) saveToken(res.slug, res.organizer_token);
			await goto(`/e/${res.slug}`);
		} catch (err) {
			error = err instanceof APIError ? err.message : String(err);
			busy = false;
		}
	}
</script>

<svelte:head>
	<title>New event — Overlap</title>
</svelte:head>

<div class="mx-auto max-w-[480px] px-4 pt-5 pb-16">
	<header class="mb-4">
		<p class="text-muted text-xs font-semibold tracking-[0.09em] uppercase">Overlap</p>
		<h1 class="mt-1.5 mb-1 text-[23px] leading-tight font-semibold tracking-tight">
			What are you scheduling?
		</h1>
		<p class="text-muted m-0 text-sm">You'll get a link to paste into the group chat.</p>
	</header>

	{#if error}
		<p class="text-bad border-bad/30 mb-3 rounded-xl border bg-white p-3 text-[13px]">{error}</p>
	{/if}

	<form class="border-line rounded-2xl border bg-white p-4" onsubmit={submit}>
		<label class="mb-3 block">
			<span class="text-muted mb-1 block text-[12.5px] font-semibold">Title</span>
			<input
				class="border-line w-full rounded-xl border p-3 text-base"
				bind:value={title}
				placeholder="Team sync"
				required
			/>
		</label>

		<label class="mb-3 block">
			<span class="text-muted mb-1 block text-[12.5px] font-semibold">Your name</span>
			<input
				class="border-line w-full rounded-xl border p-3 text-base"
				bind:value={organizerName}
				placeholder="Optional — adds you as a required attendee"
				autocomplete="name"
			/>
		</label>

		<div class="mb-3 grid grid-cols-2 gap-2">
			<label class="block">
				<span class="text-muted mb-1 block text-[12.5px] font-semibold">From</span>
				<input
					class="border-line w-full rounded-xl border p-3 text-base"
					type="date"
					bind:value={windowStart}
					required
				/>
			</label>
			<label class="block">
				<span class="text-muted mb-1 block text-[12.5px] font-semibold">To</span>
				<input
					class="border-line w-full rounded-xl border p-3 text-base"
					type="date"
					bind:value={windowEnd}
					required
				/>
			</label>
		</div>

		<div class="mb-3 grid grid-cols-2 gap-2">
			<label class="block">
				<span class="text-muted mb-1 block text-[12.5px] font-semibold">Day starts</span>
				<input
					class="border-line w-full rounded-xl border p-3 text-base"
					type="time"
					bind:value={dayStart}
					required
				/>
			</label>
			<label class="block">
				<span class="text-muted mb-1 block text-[12.5px] font-semibold">Day ends</span>
				<input
					class="border-line w-full rounded-xl border p-3 text-base"
					type="time"
					bind:value={dayEnd}
					required
				/>
			</label>
		</div>

		<label class="mb-3 block">
			<span class="text-muted mb-1 block text-[12.5px] font-semibold">Meeting length</span>
			<select
				class="border-line w-full rounded-xl border bg-white p-3 text-base"
				bind:value={slotMinutes}
			>
				{#each [15, 30, 45, 60, 90, 120] as m (m)}
					<option value={m}>{m} minutes</option>
				{/each}
			</select>
		</label>

		<label class="mb-4 block">
			<span class="text-muted mb-1 block text-[12.5px] font-semibold">Your timezone</span>
			<select
				class="border-line w-full rounded-xl border bg-white p-3 text-base"
				bind:value={timezone}
			>
				{#each allTimezones() as z (z)}
					<option value={z}>{z.replace(/_/g, ' ')}</option>
				{/each}
			</select>
			<span class="text-muted mt-1 block text-[11.5px]">
				The window above is read in this zone. Everyone else sees their own.
			</span>
		</label>

		<button
			class="bg-ink w-full rounded-xl p-3.5 text-[15px] font-semibold text-white disabled:opacity-60"
			disabled={busy}
		>
			{busy ? 'Creating…' : 'Create the link'}
		</button>
	</form>
</div>
