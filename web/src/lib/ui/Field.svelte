<script lang="ts">
	/**
	 * A labelled input.
	 *
	 * The label is a real <label> tied to the control, so a screen reader
	 * announces it and tapping the text focuses the field. Placeholder text is
	 * never used as the label: it disappears as soon as someone types, which
	 * leaves them with no way to check what the field was for.
	 */
	let {
		label,
		value = $bindable(),
		type = 'text',
		placeholder = '',
		hint = '',
		required = false,
		autocomplete = undefined,
		inputmode = undefined,
		enterkeyhint = undefined,
		onkeydown = undefined
	}: {
		label: string;
		value: string | number;
		type?: string;
		placeholder?: string;
		hint?: string;
		required?: boolean;
		autocomplete?: 'name' | 'off' | 'url';
		inputmode?: 'text' | 'url' | 'email' | 'numeric';
		enterkeyhint?: 'go' | 'done' | 'next';
		onkeydown?: (e: KeyboardEvent) => void;
	} = $props();

	const id = `f-${Math.random().toString(36).slice(2, 9)}`;
</script>

<div class="mb-4">
	<label class="mb-1.5 block text-footnote font-semibold u-muted" for={id}>{label}</label>
	<input
		{id}
		class="w-full min-h-touch rounded-[var(--radius-control)] border px-3.5 text-body"
		{type}
		{placeholder}
		{required}
		{autocomplete}
		{inputmode}
		{enterkeyhint}
		{onkeydown}
		bind:value
	/>
	{#if hint}
		<p class="mt-1.5 mb-0 text-footnote u-faint">{hint}</p>
	{/if}
</div>

<style>
	input {
		background: var(--surface);
		border-color: var(--hairline-strong);
		color: var(--ink);
		/* 17px keeps mobile browsers from zooming the page on focus, which they
		   do for anything under 16px. */
		font-family: inherit;
	}

	input::placeholder { color: var(--ink-faint); }
	input:focus { border-color: var(--accent); }
</style>
