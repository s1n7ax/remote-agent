import { afterEach, describe, expect, it, vi } from 'vitest';
import { api, ApiError, fetchTransport, setEventSource, setTransport, subscribe } from './api';
import { mockTransport } from './mock';
import type { ServerEvent } from './types';

function fakeFetch(status: number, body: unknown) {
  const calls: { url: string; init: RequestInit }[] = [];
  const fn = vi.fn(async (url: string, init: RequestInit) => {
    calls.push({ url, init });
    const text = body === undefined ? '' : JSON.stringify(body);
    return new Response(status === 204 ? null : text, { status, statusText: status === 404 ? 'Not Found' : 'OK' });
  });
  vi.stubGlobal('fetch', fn);
  return calls;
}

afterEach(() => {
  vi.unstubAllGlobals();
  setTransport(fetchTransport);
});

describe('api over fetch', () => {
  it('sends JSON bodies with the right method and path', async () => {
    const calls = fakeFetch(200, { completed: true });
    const res = await api.answer(7, 2, 'q/1', { optionIds: ['a'] });
    expect(res).toEqual({ completed: true });
    expect(calls[0].url).toBe('/api/tickets/7/rounds/2/answers/q%2F1');
    expect(calls[0].init.method).toBe('PUT');
    expect(calls[0].init.body).toBe('{"optionIds":["a"]}');
    const h = calls[0].init.headers as Record<string, string>;
    expect(h['Content-Type']).toBe('application/json');
    expect(h['X-Requested-With']).toBe('remote-agent');
  });

  it('sends X-Requested-With on every non-GET request and never on GET', async () => {
    const calls = fakeFetch(200, {});
    await api.state();
    await api.start(1);
    await api.deleteTicket(1);
    const hdr = (i: number) => (calls[i].init.headers as Record<string, string>)['X-Requested-With'];
    expect(hdr(0)).toBeUndefined();
    expect(calls[1].init.method).toBe('POST');
    expect(hdr(1)).toBe('remote-agent');
    expect(calls[1].init.body).toBeUndefined();
    expect(hdr(2)).toBe('remote-agent');
  });

  it('omits the body for bodiless calls and handles 204', async () => {
    const calls = fakeFetch(204, undefined);
    await expect(api.stopPreview()).resolves.toBeUndefined();
    expect(calls[0].url).toBe('/api/preview');
    expect(calls[0].init.method).toBe('DELETE');
    expect(calls[0].init.body).toBeUndefined();
  });

  it('builds paging queries', async () => {
    const calls = fakeFetch(200, []);
    await api.runEvents(5, 9, { after: 100, limit: 50 });
    await api.runEvents(5, 9);
    expect(calls[0].url).toBe('/api/tickets/5/runs/9/events?after=100&limit=50');
    expect(calls[1].url).toBe('/api/tickets/5/runs/9/events?limit=500');
  });

  it('keeps slashes in skill file paths', async () => {
    const calls = fakeFetch(200, { content: '' });
    await api.skillFile('my skill', 'evals/evals.json');
    expect(calls[0].url).toBe('/api/skills/my%20skill/files/evals/evals.json');
  });

  it('raises ApiError with the server error message', async () => {
    fakeFetch(409, { error: 'round already completed' });
    const err = await api.answer(1, 1, 'q', {}).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(409);
    expect(err.message).toBe('round already completed');
  });

  it('falls back to the status line when the error body is not JSON', async () => {
    vi.stubGlobal('fetch', async () => new Response('boom', { status: 500, statusText: 'Internal Server Error' }));
    await expect(api.state()).rejects.toThrow('500 Internal Server Error');
  });
});

describe('mock transport', () => {
  it('completes round 2 only on submit, once every visible question is answered', async () => {
    setTransport(mockTransport);
    const d = await api.ticket(101);
    const r2 = d.rounds.find((r) => r.n === 2)!;
    expect(r2.completedAt).toBeUndefined();
    let res = await api.answer(101, 2, 'confirm', { optionIds: ['yes'] });
    expect(res.completed).toBe(false);
    res = await api.answer(101, 2, 'proto', { optionIds: ['a'] });
    await expect(api.submitRound(101, 2)).rejects.toThrow('unanswered questions');
    res = await api.answer(101, 2, 'marks', { optionIds: ['count'] });
    expect(res.completed).toBe(false);
    expect(res.ticket.status).toBe('needs_input');
    res = await api.submitRound(101, 2);
    expect(res.completed).toBe(true);
    expect(res.ticket.status).toBe('ready');
    expect(res.ticket.stage).toBe('build');
    await expect(api.submitRound(101, 2)).rejects.toThrow('already complete');
    await expect(api.answer(101, 2, 'marks', { optionIds: ['count'] })).rejects.toThrow('already complete');
  });
});

describe('routes', () => {
  it('uses the backend paths for schedules, accounts, settings and notifications', async () => {
    const calls = fakeFetch(200, {});
    await api.updateSchedule(3, { enabled: false });
    await api.deleteSchedule(3);
    await api.patchAccount(2, { priority: 0 });
    await api.updateSettings({ dailyCapUsd: 5 });
    await api.setNotifications({ pr_ready: false });
    await api.merge(1, 52);
    await api.startPreview(101, 'A');
    await api.submitRound(7, 2);
    expect(calls.map((c) => `${c.init.method} ${c.url} ${c.init.body ?? ''}`)).toEqual([
      'PATCH /api/schedules/3 {"enabled":false}',
      'DELETE /api/schedules/3 ',
      'PATCH /api/accounts/2 {"priority":0}',
      'PUT /api/settings {"dailyCapUsd":5}',
      'PUT /api/notifications {"prefs":{"pr_ready":false}}',
      'POST /api/projects/1/pulls/52/merge {}',
      'POST /api/tickets/101/preview {"prototype":"A"}',
      'POST /api/tickets/7/rounds/2/submit ',
    ]);
  });
});

describe('subscribe', () => {
  it('shares one connection and closes it after the last listener leaves', () => {
    let opened = 0;
    let closed = 0;
    let push: (e: ServerEvent) => void = () => {};
    setEventSource((on) => {
      opened++;
      push = on;
      return () => closed++;
    });
    const a = vi.fn();
    const b = vi.fn();
    const ua = subscribe(a);
    const ub = subscribe(b);
    push({ type: 'board', projectId: 1 });
    expect(opened).toBe(1);
    expect(a).toHaveBeenCalledOnce();
    expect(b).toHaveBeenCalledOnce();
    ua();
    expect(closed).toBe(0);
    ub();
    expect(closed).toBe(1);
  });
});
