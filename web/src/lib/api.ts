// Typed client for the remote-agent HTTP API (docs/architecture.md).

import type {
  Account,
  Answer,
  AnswerResult,
  AppState,
  BenchmarkResult,
  Board,
  GitHubView,
  NotificationPrefs,
  PreviewInfo,
  Project,
  ProjectInput,
  RunEvent,
  Schedule,
  ScheduleInput,
  ServerEvent,
  Settings,
  SettingsPatch,
  SkillSummary,
  Ticket,
  TicketDetail,
  TicketImage,
  TicketInput,
  TicketPatch,
} from './types';

/** An HTTP error from the API; `message` is the server's `error` field when present. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

/** Performs one request and returns the decoded JSON body (undefined for 204). */
export type Transport = (method: string, path: string, body?: unknown) => Promise<unknown>;

export const fetchTransport: Transport = async (method, path, body) => {
  const headers: Record<string, string> = {};
  // The server rejects state-changing /api requests without this header (CSRF guard).
  if (method !== 'GET' && method !== 'HEAD') headers['X-Requested-With'] = 'remote-agent';
  // FormData sets its own multipart Content-Type with the boundary.
  const form = body instanceof FormData;
  if (body !== undefined && !form) headers['Content-Type'] = 'application/json';
  const res = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : form ? body : JSON.stringify(body),
    credentials: 'same-origin',
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body is not JSON; keep the status line
    }
    throw new ApiError(res.status, msg);
  }
  if (res.status === 204) return undefined;
  const text = await res.text();
  return text ? JSON.parse(text) : undefined;
};

let transport: Transport = fetchTransport;
let unauthorized: () => void = () => {};

/** Registers what happens when the server asks the browser to sign in. */
export function onUnauthorized(f: () => void): void {
  unauthorized = f;
}

/** Replaces the transport (mock mode, tests). */
export function setTransport(t: Transport): void {
  transport = t;
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  try {
    return (await rawCall(method, path, body)) as T;
  } catch (e) {
    if (e instanceof ApiError && e.status === 401 && path !== '/api/login') unauthorized();
    throw e;
  }
}

function rawCall<T>(method: string, path: string, body?: unknown): Promise<T> {
  return transport(method, path, body) as Promise<T>;
}

const e = encodeURIComponent;

/** Encodes a slash-separated path keeping the slashes. */
function encPath(p: string): string {
  return p.split('/').map(e).join('/');
}

type Done<K extends string> = Record<K, boolean>;

function imageForm(file: File): FormData {
  const f = new FormData();
  f.append('file', file, file.name);
  return f;
}

export const api = {
  state: () => call<AppState>('GET', '/api/state'),
  login: (token: string) => call<{ ok: boolean }>('POST', '/api/login', { token }),
  logout: () => call<{ ok: boolean }>('POST', '/api/logout'),

  // projects
  projects: () => call<Project[]>('GET', '/api/projects'),
  createProject: (p: ProjectInput) => call<Project>('POST', '/api/projects', p),
  updateProject: (id: number, p: Partial<ProjectInput>) => call<Project>('PATCH', `/api/projects/${id}`, p),
  deleteProject: (id: number) => call<Done<'deleted'>>('DELETE', `/api/projects/${id}`),
  board: (projectId: number) => call<Board>('GET', `/api/projects/${projectId}/board`),

  // tickets
  createTicket: (projectId: number, t: TicketInput) => call<Ticket>('POST', `/api/projects/${projectId}/tickets`, t),
  ticket: (id: number) => call<TicketDetail>('GET', `/api/tickets/${id}`),
  patchTicket: (id: number, p: TicketPatch) => call<Ticket>('PATCH', `/api/tickets/${id}`, p),
  deleteTicket: (id: number) => call<Done<'deleted'>>('DELETE', `/api/tickets/${id}`),
  /** Stores an image for a ticket about to be created; pass the id in TicketInput.images. */
  uploadImage: (projectId: number, file: File) => call<{ id: string }>('POST', `/api/projects/${projectId}/uploads`, imageForm(file)),
  addImage: (ticketId: number, file: File) => call<TicketImage>('POST', `/api/tickets/${ticketId}/images`, imageForm(file)),
  removeImage: (ticketId: number, n: number) => call<Done<'removed'>>('DELETE', `/api/tickets/${ticketId}/images/${n}`),
  start: (id: number) => call<Ticket>('POST', `/api/tickets/${id}/start`),
  pause: (id: number) => call<Ticket>('POST', `/api/tickets/${id}/pause`),
  resume: (id: number) => call<Ticket>('POST', `/api/tickets/${id}/resume`),
  stop: (id: number) => call<Ticket>('POST', `/api/tickets/${id}/stop`),
  retry: (id: number) => call<Ticket>('POST', `/api/tickets/${id}/retry`),
  wontDo: (id: number, reason: string) => call<Ticket>('POST', `/api/tickets/${id}/wont-do`, { reason }),
  reopen: (id: number) => call<Ticket>('POST', `/api/tickets/${id}/reopen`),
  answer: (id: number, round: number, qid: string, a: Answer) =>
    call<AnswerResult>('PUT', `/api/tickets/${id}/rounds/${round}/answers/${e(qid)}`, a),
  /** Completes a fully answered round: its answers lock and the next run starts. */
  submitRound: (id: number, round: number) => call<AnswerResult>('POST', `/api/tickets/${id}/rounds/${round}/submit`),
  message: (id: number, text: string) => call<Done<'sent'>>('POST', `/api/tickets/${id}/message`, { text }),
  /** Run log in ascending id order, `limit` events after event id `after`. */
  runEvents: (ticketId: number, runId: number, opts: { after?: number; limit?: number } = {}) => {
    const q = new URLSearchParams();
    if (opts.after) q.set('after', String(opts.after));
    q.set('limit', String(opts.limit ?? 500));
    return call<RunEvent[] | null>('GET', `/api/tickets/${ticketId}/runs/${runId}/events?${q}`);
  },
  startPreview: (ticketId: number, prototype: string) =>
    call<PreviewInfo>('POST', `/api/tickets/${ticketId}/preview`, { prototype }),
  stopPreview: () => call<Done<'stopped'>>('DELETE', '/api/preview'),

  // github
  github: (projectId: number) => call<GitHubView>('GET', `/api/projects/${projectId}/github`),
  /** Merges with the repository's default method. */
  merge: (projectId: number, pr: number, sha?: string) =>
    call<Done<'merged'>>('POST', `/api/projects/${projectId}/pulls/${pr}/merge`, sha ? { sha } : {}),

  // schedules
  schedules: (projectId: number) => call<Schedule[]>('GET', `/api/projects/${projectId}/schedules`),
  createSchedule: (projectId: number, s: ScheduleInput) =>
    call<Schedule>('POST', `/api/projects/${projectId}/schedules`, s),
  updateSchedule: (id: number, s: Partial<ScheduleInput>) => call<Schedule>('PATCH', `/api/schedules/${id}`, s),
  deleteSchedule: (id: number) => call<Done<'deleted'>>('DELETE', `/api/schedules/${id}`),

  // accounts + settings
  accounts: () => call<Account[]>('GET', '/api/accounts'),
  addAccount: (label: string, token: string, priority = 0) =>
    call<Account>('POST', '/api/accounts', { label, token, priority }),
  patchAccount: (id: number, p: { label?: string; priority?: number; clearLimited?: boolean }) =>
    call<Account>('PATCH', `/api/accounts/${id}`, p),
  deleteAccount: (id: number) => call<Done<'deleted'>>('DELETE', `/api/accounts/${id}`),
  settings: () => call<Settings>('GET', '/api/settings'),
  updateSettings: (s: SettingsPatch) => call<Settings>('PUT', '/api/settings', s),

  // skills
  skills: () => call<SkillSummary[]>('GET', '/api/skills'),
  skillFile: (name: string, path: string) => call<{ content: string }>('GET', `/api/skills/${e(name)}/files/${encPath(path)}`),
  saveSkillFile: (name: string, path: string, content: string, message: string) =>
    call<{ commit: string }>('PUT', `/api/skills/${e(name)}/files/${encPath(path)}`, { content, message }),
  deleteSkill: (name: string) => call<{ commit: string }>('DELETE', `/api/skills/${e(name)}`),
  restoreSkill: (name: string, commit: string) =>
    call<{ commit: string }>('POST', `/api/skills/${e(name)}/restore`, { commit }),
  /** Starts a background benchmark; poll benchmarkResults. */
  benchmark: (skills: string[] = []) => call<{ started: string[] }>('POST', '/api/skills/benchmark', { skills }),
  benchmarkResults: () => call<BenchmarkResult[] | null>('GET', '/api/skills/benchmark'),

  // notifications
  notifications: () => call<NotificationPrefs>('GET', '/api/notifications'),
  setNotifications: (prefs: Record<string, boolean>) => call<NotificationPrefs>('PUT', '/api/notifications', { prefs }),
  pushSubscribe: (sub: PushSubscriptionJSON) => call<Done<'subscribed'>>('POST', '/api/push/subscribe', sub),
  pushTest: () => call<Done<'sent'>>('POST', '/api/push/test', {}),
};

let artifactResolver = (id: string) => `/api/artifacts/${e(id)}`;

/** Replaces how artifact ids become URLs (mock mode). */
export function setArtifactResolver(f: (id: string) => string): void {
  artifactResolver = f;
}

/** URL of an artifact's bytes. */
export function artifactUrl(id: string): string {
  return artifactResolver(id);
}

export type EventSourceFactory = (onEvent: (ev: ServerEvent) => void) => () => void;

/** Subscribes to /api/events; returns an unsubscribe function. EventSource reconnects by itself. */
export const sseEvents: EventSourceFactory = (onEvent) => {
  const kinds = ['ticket', 'run_event', 'board'] as const;
  let es: EventSource;
  let retry: ReturnType<typeof setTimeout> | undefined;
  let closed = false;
  const handler = (type: (typeof kinds)[number]) => (m: MessageEvent<string>) => {
    try {
      onEvent({ ...JSON.parse(m.data), type } as ServerEvent);
    } catch {
      // ignore malformed events
    }
  };
  const open = () => {
    es = new EventSource('/api/events');
    for (const k of kinds) es.addEventListener(k, handler(k));
    // EventSource retries network errors itself but gives up on an HTTP error
    // (e.g. 401 before signing in); try again a little later.
    es.onerror = () => {
      if (es.readyState === EventSource.CLOSED && !closed) retry = setTimeout(open, 5000);
    };
  };
  open();
  return () => {
    closed = true;
    clearTimeout(retry);
    es.close();
  };
};

let events: EventSourceFactory = sseEvents;
const listeners = new Set<(ev: ServerEvent) => void>();
let close: (() => void) | null = null;

/** Replaces the live-event source (mock mode, tests). */
export function setEventSource(f: EventSourceFactory): void {
  close?.();
  close = null;
  events = f;
  if (listeners.size) close = events(dispatch);
}

function dispatch(ev: ServerEvent): void {
  for (const l of listeners) l(ev);
}

/** Subscribes to live server events over one shared connection; returns an unsubscribe function. */
export function subscribe(onEvent: (ev: ServerEvent) => void): () => void {
  listeners.add(onEvent);
  if (!close) close = events(dispatch);
  return () => {
    listeners.delete(onEvent);
    if (!listeners.size && close) {
      close();
      close = null;
    }
  };
}
