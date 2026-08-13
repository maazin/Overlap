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
export const load: PageLoad = async ({ params, fetch }) => {
	const res = await fetch(`${API_URL}/api/events/${encodeURIComponent(params.slug)}`);

	if (res.status === 404) {
		error(404, 'That link has expired or never existed.');
	}
	if (!res.ok) {
		// Rendering the shell and letting the client retry beats a hard failure:
		// the API being briefly unreachable should not make the link look dead.
		return { event: null as EventView | null, slug: params.slug };
	}

	return { event: (await res.json()) as EventView, slug: params.slug };
};
