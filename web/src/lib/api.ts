import { env } from '$env/dynamic/public';

export const API_URL = env.PUBLIC_API_URL ?? 'http://localhost:8080';

export type Tier = 'preferred' | 'ok' | 'if_needed' | 'no';
export type Block = 'morning' | 'afternoon' | 'evening';
export type Role = 'required' | 'optional';

export type DSTNote = {
	date: string;
	local_time: string;
	reason: 'nonexistent' | 'ambiguous';
	detail: string;
};

export type ParticipantView = {
	id: string;
	name: string;
	role: Role;
	is_organizer: boolean;
	responded: boolean;
};

export type ResponseView = {
	slot_start: string;
	tier: Tier;
	source: 'manual' | 'coarse' | 'calendar';
};

export type SelfView = {
	participant_id: string;
	name: string;
	timezone: string;
	role: Role;
	is_organizer: boolean;
	responded: boolean;
	calendar_source?: string;
	responses: ResponseView[];
};

export type EventView = {
	slug: string;
	title: string;
	timezone: string;
	window_start: string;
	window_end: string;
	day_start: string;
	day_end: string;
	slot_minutes: number;
	status: string;
	slots: string[];
	dst_notes?: DSTNote[];
	expires_at: string;
	participants: ParticipantView[];
	you?: SelfView;
};

/** APIError carries the server's message so a form can show it verbatim. */
export class APIError extends Error {
	constructor(
		readonly status: number,
		message: string
	) {
		super(message);
		this.name = 'APIError';
	}
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
	let res: Response;
	try {
		res = await fetch(`${API_URL}${path}`, {
			...init,
			headers: { 'Content-Type': 'application/json', ...(init.headers ?? {}) }
		});
	} catch {
		// fetch rejects on network failure and on a CORS rejection, with no
		// detail in either case. Saying "unreachable" is honest; guessing which
		// one it was would not be.
		throw new APIError(0, `Could not reach the API at ${API_URL}.`);
	}

	if (!res.ok) {
		let msg = `Request failed (${res.status})`;
		try {
			const body = await res.json();
			if (typeof body?.error === 'string') msg = body.error;
		} catch {
			// A non-JSON error body is not worth a second failure mode.
		}
		throw new APIError(res.status, msg);
	}

	if (res.status === 204) return undefined as T;
	return res.json();
}

export type CreateEventBody = {
	title: string;
	timezone: string;
	window_start: string;
	window_end: string;
	day_start?: string;
	day_end?: string;
	slot_minutes?: number;
	organizer_name?: string;
};

export type CreateEventResult = {
	slug: string;
	organizer_token?: string;
	participant_id?: string;
};

export const createEvent = (body: CreateEventBody) =>
	request<CreateEventResult>('/api/events', { method: 'POST', body: JSON.stringify(body) });

export const getEvent = (slug: string, token?: string | null) =>
	request<EventView>(`/api/events/${encodeURIComponent(slug)}`, {
		headers: token ? { 'X-Participant-Token': token } : {}
	});

export type JoinResult = {
	participant_id: string;
	token: string;
	name: string;
	timezone: string;
	role: Role;
};

export const joinEvent = (slug: string, body: { name: string; timezone: string }) =>
	request<JoinResult>(`/api/events/${encodeURIComponent(slug)}/participants`, {
		method: 'POST',
		body: JSON.stringify(body)
	});

export type PutResponsesBody = {
	coarse: { date: string; block: Block; tier: Tier }[];
	slots: { slot_start: string; tier: Tier }[];
	timezone?: string;
};

export const putResponses = (slug: string, token: string, body: PutResponsesBody) =>
	request<{ saved: number }>(`/api/events/${encodeURIComponent(slug)}/responses`, {
		method: 'PUT',
		headers: { 'X-Participant-Token': token },
		body: JSON.stringify(body)
	});

/**
 * One scored slot.
 *
 * There is no score field, deliberately. The composite orders the list on the
 * server and never leaves it, because a number next to a time reads as
 * precision the model does not have.
 */
export type RankedSlot = {
	slot_start: string;
	coverage: number;
	total: number;
	eliminated: boolean;
	eliminated_by?: string[];
	excludes?: string[];
	unknown?: string[];
	unsociable: boolean;
};

/** The distinct situations an organizer can be in. Each implies a different
 * next action, which is why the server names the state rather than leaving the
 * client to infer it from booleans. */
export type Verdict =
	| 'decided'
	| 'decidable'
	| 'waiting_on_required'
	| 'waiting_on_relevant'
	| 'tied'
	| 'no_slots';

export type DominanceView = {
	verdict: Verdict;
	decidable: boolean;
	leader?: string;
	blocking_required?: string[];
	relevant?: string[];
};

export type SolveView = {
	slug: string;
	status: string;
	responded: number;
	total: number;
	ranked: RankedSlot[];
	dominance: DominanceView;
	decided_slot_start?: string;
};

export const solve = (slug: string) =>
	request<SolveView>(`/api/events/${encodeURIComponent(slug)}/solve`);

export const decide = (slug: string, token: string, slotStart: string, force = false) =>
	request<{ slug: string; status: string; decided_slot_start: string }>(
		`/api/events/${encodeURIComponent(slug)}/decide`,
		{
			method: 'POST',
			headers: { 'X-Participant-Token': token },
			body: JSON.stringify({ slot_start: slotStart, force })
		}
	);

export const reopen = (slug: string, token: string) =>
	request<{ slug: string; status: string }>(`/api/events/${encodeURIComponent(slug)}/reopen`, {
		method: 'POST',
		headers: { 'X-Participant-Token': token }
	});

export type ConnectICSResult = {
	source: 'ics';
	busy_blocks: number;
	slots_blocked: number;
	fetched_at: string;
};

export const connectICS = (slug: string, token: string, url: string) =>
	request<ConnectICSResult>(`/api/events/${encodeURIComponent(slug)}/calendar/ics`, {
		method: 'POST',
		headers: { 'X-Participant-Token': token },
		body: JSON.stringify({ url })
	});

export const disconnectCalendar = (slug: string, token: string) =>
	request<{ source: string }>(`/api/events/${encodeURIComponent(slug)}/calendar`, {
		method: 'DELETE',
		headers: { 'X-Participant-Token': token }
	});

/** The .ics is fetched by the browser directly, so this is a plain URL. */
export const icsURL = (slug: string) =>
	`${API_URL}/api/events/${encodeURIComponent(slug)}/decided.ics`;
