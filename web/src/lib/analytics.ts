/**
 * URL redaction for analytics.
 *
 * Every slug in this product is a credential. There are no accounts and no
 * passwords: holding `/e/mkg3nhce` is what authorises reading an event, seeing
 * who has answered and joining it. The README states that data goes away when
 * a link expires, and shipping the links themselves to a third party is the
 * same disclosure by a slower route.
 *
 * The SvelteKit integration reports both the route pattern and the resolved
 * path, and the pattern is the half worth having. Knowing that `/e/[slug]` was
 * viewed four hundred times is the analytics question. Knowing *which* events
 * were viewed answers no question worth the exposure.
 */

/** Path segments that carry a slug, keyed by the prefix that introduces them. */
const SLUG_ROUTES = /^\/(e|g)\/[^/]+/;

/**
 * Replaces the slug in a URL with the route pattern, leaving everything else
 * intact so `/e/abc123/results` still reports as a results view.
 *
 * Query strings go entirely. Nothing in this app puts anything in one, which
 * makes dropping them free, and a redactor that only handles what today's code
 * does is a redactor that leaks the first time somebody adds a parameter.
 */
export function redactURL(raw: string): string {
	try {
		const url = new URL(raw);
		url.pathname = url.pathname.replace(SLUG_ROUTES, '/$1/[slug]');
		url.search = '';
		url.hash = '';
		return url.toString();
	} catch {
		// An unparseable URL is not something to guess at. Returning a constant
		// loses one data point; returning the original could leak the thing
		// this function exists to remove.
		return '/[unparseable]';
	}
}
