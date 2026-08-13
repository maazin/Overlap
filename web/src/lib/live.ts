import { API_URL } from './api';

/**
 * How long the stream may stay silent before we assume it is dead.
 *
 * The server sends a ping every 15 seconds, so this is three missed beats.
 * Tighter than that and a phone waking from sleep redials for no reason.
 */
const STALE_AFTER_MS = 45_000;
const WATCHDOG_INTERVAL_MS = 5_000;

/**
 * Opens a live connection to an event and calls `onChange` whenever anything
 * about it changes.
 *
 * The payload is ignored on purpose. Every message means "refetch", which makes
 * a dropped or coalesced event cost one stale second rather than leaving the
 * page holding a half-applied update, and it means reconnecting needs no
 * replay: the refetch after reconnect *is* the recovery.
 *
 * Recovery does not rely on the browser noticing the disconnect, because it
 * does not reliably notice. A killed server can leave EventSource sitting in
 * the OPEN state with no error event and no reconnect attempt, so a client that
 * waits to be told stays stale forever. Instead the server pings on a timer and
 * silence is treated as death: the watchdog below tears the connection down and
 * builds a new one.
 *
 * Returns the teardown function. Callers must invoke it, or a navigated-away
 * page keeps a connection open and the server keeps a subscriber for it.
 */
export function subscribe(slug: string, onChange: () => void): () => void {
	const url = `${API_URL}/api/events/${encodeURIComponent(slug)}/stream`;

	let source: EventSource | null = null;
	let lastSeen = Date.now();
	let closed = false;

	function connect() {
		if (closed) return;

		source = new EventSource(url);

		// Any traffic at all, including the heartbeat, proves the stream is
		// alive. Only the real events trigger a refetch.
		const seen = () => (lastSeen = Date.now());
		source.addEventListener('ping', seen);

		for (const name of ['response_submitted', 'decided', 'reopened']) {
			source.addEventListener(name, () => {
				seen();
				onChange();
			});
		}

		source.addEventListener('open', () => {
			seen();
			// Refetch on every successful connect, not just the first. This is
			// what makes a reconnect show correct state rather than whatever
			// was on screen when the connection dropped.
			onChange();
		});

		source.addEventListener('error', () => {
			// EventSource retries on its own when it can. Recording the failure
			// lets the watchdog step in when it cannot.
			if (source?.readyState === EventSource.CLOSED) reconnect();
		});
	}

	function reconnect() {
		if (closed) return;
		source?.close();
		source = null;
		lastSeen = Date.now();
		connect();
	}

	const watchdog = setInterval(() => {
		if (closed) return;
		if (Date.now() - lastSeen > STALE_AFTER_MS) reconnect();
	}, WATCHDOG_INTERVAL_MS);

	// Coming back to a backgrounded tab is the most common way to arrive at a
	// stale page: phones suspend timers and sockets while the screen is off.
	const onVisible = () => {
		if (document.visibilityState !== 'visible') return;
		onChange();
		if (Date.now() - lastSeen > STALE_AFTER_MS) reconnect();
	};
	document.addEventListener('visibilitychange', onVisible);

	connect();

	return () => {
		if (closed) return;
		closed = true;
		clearInterval(watchdog);
		document.removeEventListener('visibilitychange', onVisible);
		source?.close();
		source = null;
	};
}
