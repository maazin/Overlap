<script lang="ts">
	import { page } from '$app/state';
	import { browser } from '$app/environment';
	import {
		getGroup,
		joinGroup,
		claimGroupMember,
		connectGroupICS,
		disconnectGroupCalendar,
		APIError,
		type GroupView
	} from '$lib/api';
	import { loadGroupToken, saveGroupToken, detectTimezone } from '$lib/identity';
	import Loading from '$lib/ui/Loading.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Card from '$lib/ui/Card.svelte';
	import Field from '$lib/ui/Field.svelte';
	import PageHeader from '$lib/ui/PageHeader.svelte';

	const slug = page.params.slug!;

	let group = $state<GroupView | null>(null);
	let token = $state<string | null>(null);
	let error = $state('');
	let busy = $state(false);

	let joinName = $state('');
	let joinOpen = $state(false);

	let calendarURL = $state('');
	let calendarOpen = $state(false);
	let calendarNote = $state('');

	const you = $derived(group?.you ?? null);
	const unclaimed = $derived((group?.members ?? []).filter((m) => !m.claimed));

	async function load() {
		try {
			token = loadGroupToken(slug);
			group = await getGroup(slug, token);
			if (!group.you) token = null;
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}

	async function claim(memberID: string) {
		busy = true;
		error = '';
		try {
			const res = await claimGroupMember(slug, memberID);
			token = res.token;
			saveGroupToken(slug, res.token);
			group = await getGroup(slug, token);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			busy = false;
		}
	}

	async function join() {
		if (!joinName.trim()) {
			error = 'A name is needed.';
			return;
		}
		busy = true;
		error = '';
		try {
			const res = await joinGroup(slug, joinName.trim(), browser ? detectTimezone() : 'UTC');
			token = res.token;
			saveGroupToken(slug, res.token);
			group = await getGroup(slug, token);
			joinOpen = false;
		} catch (e) {
			error = e instanceof APIError ? e.message : String(e);
		} finally {
			busy = false;
		}
	}

	async function linkCalendar() {
		if (!token || !calendarURL.trim()) return;
		busy = true;
		error = '';
		try {
			const res = await connectGroupICS(slug, token, calendarURL.trim());
			calendarNote = `Read your calendar, ${res.busy_blocks} busy block${res.busy_blocks === 1 ? '' : 's'} found in the next 90 days.`;
			calendarOpen = false;
			group = await getGroup(slug, token);
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
			await disconnectGroupCalendar(slug, token);
			calendarNote = '';
			group = await getGroup(slug, token);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			busy = false;
		}
	}

	function eventStatusLabel(status: string): string {
		if (status === 'decided') return 'decided';
		if (status === 'expired') return 'expired';
		return 'open';
	}

	if (browser) load();
</script>

<svelte:head>
	<title>{group ? group.name : 'Group'}</title>
</svelte:head>

<div class="u-column pt-8 pb-16">
	{#if !group}
		<Loading />
	{:else}
		<PageHeader title={group.name}>
			{group.members.length} member{group.members.length === 1 ? '' : 's'}
		</PageHeader>

		{#if error}
			<div class="mb-5">
				<Card tone="critical"><p class="m-0 text-subhead">{error}</p></Card>
			</div>
		{/if}

		{#if you}
			<div class="mb-4">
				<Button variant="primary" href="/g/{slug}/new">Schedule something</Button>
			</div>
		{:else}
			<div class="mb-4">
				<Card>
					<h2 class="m-0 mb-1 text-heading font-semibold">Which one are you?</h2>
					<p class="mt-0 mb-4 text-subhead u-muted">
						No account. Pick your name from the roster below, or join as somebody new.
					</p>
					{#if unclaimed.length > 0}
						<div class="mb-4 flex flex-col gap-2">
							{#each unclaimed as m (m.id)}
								<Button onclick={() => claim(m.id)} disabled={busy}>{m.name}</Button>
							{/each}
						</div>
					{/if}
					{#if joinOpen}
						<Field label="Your name" bind:value={joinName} placeholder="Not on the list" />
						<Button variant="primary" onclick={join} disabled={busy}>Join the group</Button>
					{:else}
						<Button variant="quiet" onclick={() => (joinOpen = true)}>
							I'm not on this list
						</Button>
					{/if}
				</Card>
			</div>
		{/if}

		{#if you}
			<div class="mb-4">
				{#if !you.has_calendar}
					<Card>
						<h2 class="m-0 mb-1 text-heading font-semibold">Connect a calendar</h2>
						<p class="mt-0 mb-4 text-subhead u-muted">
							Once everyone in the group has connected a calendar, new events can suggest a time
							before anyone has to answer anything.
						</p>
						{#if calendarOpen}
							<Field
								label="Calendar address"
								bind:value={calendarURL}
								placeholder="webcal:// or https://"
								inputmode="url"
								autocomplete="off"
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
							{calendarNote || 'Your free and busy times are available to this group.'}
						</p>
						<div class="mt-4">
							<Button variant="quiet" onclick={unlinkCalendar} disabled={busy}>Disconnect</Button>
						</div>
					</Card>
				{/if}
			</div>
		{/if}

		<h2 class="mt-8 mb-3 text-heading font-semibold">Members</h2>
		<div class="mb-8">
			<Card>
				{#each group.members as m (m.id)}
					<div class="roster">
						<span class="text-subhead">
							{m.name}
							{#if m.role === 'required'}<span class="ml-1 text-caption u-faint">required</span>{/if}
						</span>
						<span class="text-footnote u-faint">
							{m.claimed ? (m.has_calendar ? 'calendar connected' : 'joined') : 'not yet joined'}
						</span>
					</div>
				{/each}
			</Card>
		</div>

		<h2 class="mb-3 text-heading font-semibold">Past events</h2>
		{#if group.events.length === 0}
			<p class="text-subhead u-muted">Nothing scheduled yet.</p>
		{:else}
			<Card>
				{#each group.events as ev (ev.slug)}
					<a class="event-row" href="/e/{ev.slug}/results">
						<span class="text-subhead font-semibold">{ev.title}</span>
						<span class="text-footnote u-faint">{eventStatusLabel(ev.status)}</span>
					</a>
				{/each}
			</Card>
		{/if}
	{/if}
</div>

<style>
	.roster {
		display: flex;
		justify-content: space-between;
		align-items: baseline;
		gap: 0.75rem;
		padding: 0.625rem 0;
		border-bottom: 1px solid var(--hairline);
	}
	.roster:last-child { border-bottom: 0; }

	.event-row {
		display: flex;
		justify-content: space-between;
		align-items: baseline;
		gap: 0.75rem;
		padding: 0.625rem 0;
		border-bottom: 1px solid var(--hairline);
		color: inherit;
		text-decoration: none;
	}
	.event-row:last-child { border-bottom: 0; }
	.event-row:hover { text-decoration: underline; }
</style>
