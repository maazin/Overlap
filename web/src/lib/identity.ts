import { browser } from '$app/environment';

/**
 * Participant tokens are stored per event slug, never globally.
 *
 * Scoping matters: the server refuses a token outside the event it was minted
 * for, so a single shared key would just mean sending a token that is always
 * rejected, and would leak which other events a device has joined to any code
 * running on the page.
 */
const key = (slug: string) => `overlap:token:${slug}`;

export function loadToken(slug: string): string | null {
	if (!browser) return null;
	try {
		return localStorage.getItem(key(slug));
	} catch {
		// Safari in private mode throws on access rather than returning null.
		// Losing the token means answering again, which is survivable; throwing
		// here would white-screen the page instead.
		return null;
	}
}

export function saveToken(slug: string, token: string): void {
	if (!browser) return;
	try {
		localStorage.setItem(key(slug), token);
	} catch {
		// Storage full or blocked. The response still submits; only the ability
		// to come back and edit is lost.
	}
}

export function clearToken(slug: string): void {
	if (!browser) return;
	try {
		localStorage.removeItem(key(slug));
	} catch {
		/* nothing useful to do */
	}
}

/**
 * The viewer's IANA zone, as reported by the browser.
 *
 * Falls back to UTC only when the browser refuses to say, which is rare enough
 * that guessing anything cleverer would be worse than being obviously wrong.
 */
export function detectTimezone(): string {
	try {
		return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
	} catch {
		return 'UTC';
	}
}

/** Every IANA zone the browser knows, for the manual override picker. */
export function allTimezones(): string[] {
	try {
		const supported = (
			Intl as unknown as { supportedValuesOf?: (k: string) => string[] }
		).supportedValuesOf?.('timeZone');
		if (supported?.length) return supported;
	} catch {
		/* fall through */
	}
	return [detectTimezone(), 'UTC'];
}

/**
 * Group tokens are stored separately from event tokens, keyed by group slug.
 *
 * The two must never collide: a group token authenticates standing
 * membership, an event token authenticates one response, and the server
 * checks each against a different resource entirely. Keeping them in
 * different keys is what lets the event page look up "do I already belong to
 * the group this event came from" without any risk of reading the wrong kind
 * of token by accident.
 */
const groupKey = (slug: string) => `overlap:group-token:${slug}`;

export function loadGroupToken(slug: string): string | null {
	if (!browser) return null;
	try {
		return localStorage.getItem(groupKey(slug));
	} catch {
		return null;
	}
}

export function saveGroupToken(slug: string, token: string): void {
	if (!browser) return;
	try {
		localStorage.setItem(groupKey(slug), token);
	} catch {
		/* the response still works; only cross-device recall is lost */
	}
}
