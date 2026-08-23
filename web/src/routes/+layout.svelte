<script lang="ts">
	import favicon from '$lib/assets/favicon.svg';
	import '../app.css';

	let { children } = $props();
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
	<!-- Tells the browser both appearances are handled, so form controls and
	     scrollbars are drawn to match rather than staying light on a dark page. -->
	<meta name="color-scheme" content="light dark" />
</svelte:head>

{@render children()}
