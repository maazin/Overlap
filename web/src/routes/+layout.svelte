<script lang="ts">
	import { dev } from '$app/environment';
	import { injectAnalytics } from '@vercel/analytics/sveltekit';
	import favicon from '$lib/assets/favicon.svg';
	import { redactURL } from '$lib/analytics';
	import '../app.css';

	// beforeSend is not optional here. The SvelteKit integration reports the
	// resolved pathname alongside the route pattern, and in this product the
	// slug in that pathname is the credential: anyone holding it can open the
	// event, read the roster and join. See $lib/analytics.
	injectAnalytics({
		mode: dev ? 'development' : 'production',
		beforeSend: (event) => ({ ...event, url: redactURL(event.url) })
	});

	let { children } = $props();
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
	<!-- Tells the browser both appearances are handled, so form controls and
	     scrollbars are drawn to match rather than staying light on a dark page. -->
	<meta name="color-scheme" content="light dark" />
</svelte:head>

{@render children()}
