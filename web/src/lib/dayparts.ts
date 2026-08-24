import type { Block, Tier } from './api';

/**
 * Boundaries between the three coarse blocks, as local wall-clock hours.
 *
 * These mirror api/internal/dayparts/dayparts.go. They must agree: the server
 * expands a coarse tap using the responder's zone, so if the client draws a
 * slot under Afternoon while the server files it under Morning, the tap lands
 * on a different set of slots than the one the responder saw.
 */
export const AFTERNOON_START_HOUR = 12;
export const EVENING_START_HOUR = 17;

export const BLOCKS: Block[] = ['morning', 'afternoon', 'evening'];

export const BLOCK_LABELS: Record<Block, string> = {
	morning: 'Morning',
	afternoon: 'Afternoon',
	evening: 'Evening'
};

export function blockForHour(hour: number): Block {
	if (hour < AFTERNOON_START_HOUR) return 'morning';
	if (hour < EVENING_START_HOUR) return 'afternoon';
	return 'evening';
}

/** A coordinate on the coarse grid: one local date, one block. */
export type Cell = { date: string; block: Block };

export const cellKey = (c: Cell) => `${c.date}|${c.block}`;

/**
 * A cache of Intl.DateTimeFormat instances, keyed by locale, zone and the
 * option set that matters here (a short tag, not the whole options object).
 *
 * Constructing a DateTimeFormat is the expensive part of formatting a date --
 * it resolves locale data and a time zone -- while calling .format() on an
 * existing instance is cheap. Every function below calls one of these on
 * every slot, and a week-long event has 100-200 slots re-formatted on every
 * grid render, so building a fresh formatter per call rather than per
 * (locale, zone, kind) turned "render the grid" into "construct several
 * hundred formatters" for no reason.
 */
const formatterCache = new Map<string, Intl.DateTimeFormat>();

function formatter(
	kind: string,
	locale: string | undefined,
	tz: string,
	options: Intl.DateTimeFormatOptions
): Intl.DateTimeFormat {
	const key = `${kind}|${locale ?? ''}|${tz}`;
	let f = formatterCache.get(key);
	if (!f) {
		f = new Intl.DateTimeFormat(locale, { ...options, timeZone: tz });
		formatterCache.set(key, f);
	}
	return f;
}

/**
 * Formats an instant as a YYYY-MM-DD local date in `tz`.
 *
 * Built from Intl parts rather than toISOString, which would give the UTC date
 * and quietly put late-evening slots on the wrong day for anyone west of
 * Greenwich.
 */
export function localDate(instant: Date, tz: string): string {
	const parts = formatter('date-ymd', 'en-CA', tz, {
		year: 'numeric',
		month: '2-digit',
		day: '2-digit'
	}).formatToParts(instant);

	const get = (t: string) => parts.find((p) => p.type === t)?.value ?? '';
	return `${get('year')}-${get('month')}-${get('day')}`;
}

/** The local hour of an instant in `tz`, 0-23. */
export function localHour(instant: Date, tz: string): number {
	const v = formatter('hour', 'en-GB', tz, { hour: '2-digit', hour12: false }).format(instant);
	// en-GB renders midnight as "24" in some engines; normalise it.
	return Number(v) % 24;
}

export function cellFor(instant: Date, tz: string): Cell {
	return { date: localDate(instant, tz), block: blockForHour(localHour(instant, tz)) };
}

export type DayGroup = {
	date: string;
	weekday: string;
	dayLabel: string;
	blocks: Record<Block, Date[]>;
};

/**
 * Groups slots into days and blocks in the viewer's zone, preserving order.
 *
 * Days with no slots at all never appear, so an event whose window is
 * weekdays-only does not render empty weekend rows.
 */
export function groupByDay(slots: Date[], tz: string): DayGroup[] {
	const byDate = new Map<string, DayGroup>();

	for (const s of slots) {
		const date = localDate(s, tz);
		let g = byDate.get(date);
		if (!g) {
			g = {
				date,
				weekday: formatter('weekday', undefined, tz, { weekday: 'short' }).format(s),
				dayLabel: formatter('day-label', undefined, tz, { month: 'short', day: 'numeric' }).format(
					s
				),
				blocks: { morning: [], afternoon: [], evening: [] }
			};
			byDate.set(date, g);
		}
		g.blocks[blockForHour(localHour(s, tz))].push(s);
	}

	return [...byDate.values()].sort((a, b) => a.date.localeCompare(b.date));
}

/** Renders a slot's start time in the viewer's zone, e.g. "9:30 am". */
export function slotLabel(instant: Date, tz: string): string {
	return formatter('slot-label', undefined, tz, { hour: 'numeric', minute: '2-digit' }).format(
		instant
	);
}

/** Tier vocabulary shared with the server. */
export const TIER_LABELS: Record<Tier, string> = {
	preferred: 'Great',
	ok: 'Works',
	if_needed: 'If needed',
	no: 'No'
};

/**
 * The coarse pass cycles through three states. Untapped means no, which is why
 * the cycle returns to undefined rather than to an explicit tier.
 */
export const COARSE_CYCLE: (Tier | undefined)[] = [undefined, 'ok', 'if_needed'];

export function nextCoarse(current: Tier | undefined): Tier | undefined {
	const i = COARSE_CYCLE.findIndex((t) => t === current);
	return COARSE_CYCLE[(i + 1) % COARSE_CYCLE.length];
}

/**
 * The fine pass cycles all four tiers. It is the only place `preferred` can be
 * expressed, which is what makes the preference tiers more than decoration.
 */
export const FINE_CYCLE: Tier[] = ['preferred', 'ok', 'if_needed', 'no'];

export function nextFine(current: Tier): Tier {
	const i = FINE_CYCLE.indexOf(current);
	return FINE_CYCLE[(i + 1) % FINE_CYCLE.length];
}
