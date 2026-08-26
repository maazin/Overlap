import { error } from '@sveltejs/kit';
import { API_URL, type EventView } from '$lib/api';
import type { PageLoad } from './$types';

/**
 * Loads the event during server rendering.
 *
 * This exists for the link preview. A bare URL pasted into a group chat gets
 * ignored, and Open Graph tags only work if they are in the HTML the crawler
 * receives -- a crawler runs no JavaScript, so a client-side fetch is invisible
 * to it. Since the group chat is the entire distribution channel, this is
 * growth work rather than polish.
 *
 * It deliberately sends no participant token. Tokens live in localStorage,
 * which the server cannot see, and the "you" half of the page stays a
 * client-side fetch so that the cross-origin path the real client uses is still
 * exercised on every load.
 */

/**
 * How long server rendering waits for the API before giving up on the preview.
 *
 * Deliberately short, and shorter than the hosting platform's own function
 * timeout. On a free tier the API sleeps when idle and the first request wakes
 * it, which can take the better part of a minute. Waiting that out here would
 * hit the platform's limit and return a 500 error page, so the visitor who
 * finally reached a working link would be told the link is broken.
 *
 * Giving up early costs the Open Graph tags on that one request, because the
 * crawler gets a shell. The person gets a page that loads, and their browser
 * has no timeout to trip: the client fetch below can wait for the wake-up as
 * long as it needs while the loading state explains itself.
 */
const SSR_TIMEOUT_MS = 3000;

export const load: PageLoad = async ({ params, fetch }) => {
	// The shell, used for every path where the API did not answer in time or at
	// all. Rendering it and letting the client retry beats a hard failure: an
	// API that is briefly unreachable should not make the link look dead.
	const shell = { event: null as EventView | null, slug: params.slug };

	let res: Response;
	try {
		res = await fetch(`${API_URL}/api/events/${encodeURIComponent(params.slug)}`, {
			signal: AbortSignal.timeout(SSR_TIMEOUT_MS)
		});
	} catch {
		// fetch rejects on a refused connection, a DNS failure and on the abort
		// above. All three mean the same thing here, and none of them is
		// evidence that the event does not exist.
		return shell;
	}

	if (res.status === 404) {
		error(404, 'That link has expired or never existed.');
	}
	if (!res.ok) {
		return shell;
	}

	try {
		return { event: (await res.json()) as EventView, slug: params.slug };
	} catch {
		// A truncated or non-JSON body. Same handling: the client will refetch.
		return shell;
	}
};
