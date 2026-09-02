<script lang="ts">
	import Button from '$lib/ui/Button.svelte';

	const title = "Overlap, everyone's free time in fifteen seconds";
	const description =
		'Group scheduling without the grid. Answer in six taps, with no account. Overlap works out when a time is already settled.';

	/**
	 * Three claims, in the order somebody decides in: can I be bothered, is it
	 * better than what I use now, and what does it cost me.
	 *
	 * Laid out as a list rather than a row of cards. Three cards side by side
	 * gives every point identical visual weight and reads as a template; a
	 * stack lets the eye run down them and stop at whichever one matters.
	 */
	const points = [
		{
			heading: 'Answer in six taps',
			body: 'Tap the day parts that work, then confirm the hours inside them. No account, no email, and no grid to paint.'
		},
		{
			heading: 'It tells you when to stop waiting',
			body: 'Overlap works out whether the people who have not replied can still change the answer. Usually they cannot, and you can book the room now.'
		},
		{
			heading: 'Calendars stay free and busy',
			body: 'Connect one and Overlap reads the hours you are already booked. It never fetches, stores, or logs what any of it is for.'
		}
	];
</script>

<svelte:head>
	<title>{title}</title>
	<meta name="description" content={description} />
	<meta property="og:type" content="website" />
	<meta property="og:site_name" content="Overlap" />
	<meta property="og:title" content={title} />
	<meta property="og:description" content={description} />
	<meta name="twitter:card" content="summary" />
</svelte:head>

<main class="u-column py-14">
	<p class="m-0 text-caption font-semibold tracking-[0.14em] uppercase u-faint">Overlap</p>

	<h1 class="masthead u-serif mt-3 mb-0 font-semibold">Everyone's free time, in fifteen seconds.</h1>

	<p class="mt-5 mb-9 text-body u-muted">
		Group scheduling that ends as soon as the answer is clear, however many people have replied.
	</p>

	<Button variant="primary" href="/new">Create an event</Button>
	<p class="mt-3 mb-0 text-center text-footnote u-faint">
		You get a link. Paste it into the group chat.
	</p>

	<!-- Separator lines rather than boxes. HIG's layout guidance names both as
	     ways to group related items, and lines add no enclosed shapes to a page
	     whose only real job is the button above. The last rule is dropped: a
	     line with nothing under it reads as a list that got cut off. -->
	<section class="mt-16 border-t u-hairline">
		{#each points as point (point.heading)}
			<div class="border-b py-7 u-hairline last:border-b-0">
				<h2 class="m-0 text-heading font-semibold tracking-tight">{point.heading}</h2>
				<p class="mt-2 mb-0 text-callout u-muted">{point.body}</p>
			</div>
		{/each}
	</section>
</main>

<style>
	/* Scales with the viewport instead of snapping at a breakpoint, and stays
	   inside a range that keeps the line count stable on a phone.

	   The negative tracking is an optical correction rather than a style: a
	   serif set this large carries visibly more letterspacing than it appears
	   to at body size, and left alone the headline reads loose. */
	.masthead {
		font-size: clamp(2.25rem, 1.6rem + 3vw, 3rem);
		line-height: 1.04;
		letter-spacing: -0.022em;
		text-wrap: balance;
	}
</style>
