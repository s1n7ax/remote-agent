<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api, subscribe } from '../lib/api';
  import { app } from '../lib/app.svelte';
  import { clock, errorText } from '../lib/format';
  import type { Run, RunEvent } from '../lib/types';
  import Icon from './Icon.svelte';

  let { ticketId, runs }: { ticketId: number; runs: Run[] } = $props();

  const ordered = $derived([...runs].sort((a, b) => b.id - a.id));
  const live = $derived(ordered.find((r) => ['queued', 'running', 'paused'].includes(r.status)));
  let selected = $state<number | null>(null);
  const runId = $derived(selected ?? ordered[0]?.id ?? null);
  const run = $derived(ordered.find((r) => r.id === runId));

  let events = $state<RunEvent[]>([]);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let message = $state('');
  let sending = $state(false);
  let box = $state<HTMLElement>();
  let stick = true;

  const PAGE = 500;
  const TAIL = 300;
  let showAll = $state(false);
  const hidden = $derived(showAll ? 0 : Math.max(0, events.length - TAIL));
  const shown = $derived(hidden ? events.slice(hidden) : events);

  /** Loads the run's whole log, page by page (the API pages forward with ?after=). */
  async function loadAll(id: number) {
    loading = true;
    showAll = false;
    try {
      let all: RunEvent[] = [];
      for (;;) {
        const page = (await api.runEvents(ticketId, id, { after: all.at(-1)?.id, limit: PAGE })) ?? [];
        if (id !== runId) return;
        all = all.concat(page);
        if (page.length < PAGE) break;
      }
      // Live events may have arrived while paging.
      const last = all.at(-1)?.id ?? 0;
      events = all.concat(events.filter((x) => x.runId === id && x.id > last));
      error = null;
      await tick();
      scrollEnd();
    } catch (e) {
      error = errorText(e);
    } finally {
      loading = false;
    }
  }

  async function revealEarlier() {
    const h = box?.scrollHeight ?? 0;
    showAll = true;
    await tick();
    if (box) box.scrollTop += box.scrollHeight - h;
  }

  $effect(() => {
    events = [];
    if (runId !== null) loadAll(runId);
  });

  function scrollEnd() {
    if (box && stick) box.scrollTop = box.scrollHeight;
  }

  onMount(() =>
    subscribe(async (ev) => {
      if (ev.type !== 'run_event' || ev.ticketId !== ticketId) return;
      if (ev.data.runId !== runId) return;
      if (events.some((x) => x.id === ev.data.id)) return;
      events.push(ev.data);
      await tick();
      scrollEnd();
    }),
  );

  function onscroll() {
    if (box) stick = box.scrollHeight - box.scrollTop - box.clientHeight < 60;
  }

  async function send(e: SubmitEvent) {
    e.preventDefault();
    const text = message.trim();
    if (!text) return;
    sending = true;
    const ok = await app.attempt(async () => {
      await api.message(ticketId, text);
      return true;
    });
    sending = false;
    if (!ok) return;
    message = '';
    stick = true;
  }

  function firstLine(s: string) {
    const i = s.indexOf('\n');
    return i < 0 ? s : s.slice(0, i) + ' …';
  }
</script>

<div class="logtab">
  {#if ordered.length > 1}
    <div class="runs" role="tablist" aria-label="Runs">
      {#each ordered as r (r.id)}
        <button class={{ on: r.id === runId }} type="button" role="tab" aria-selected={r.id === runId} onclick={() => (selected = r.id)}>
          {r.stage}<span class="muted"> · {r.status}</span>
        </button>
      {/each}
    </div>
  {/if}

  {#if !run}
    <div class="empty"><p>No runs yet.</p><p class="small">Start the ticket to see the agent work here.</p></div>
  {:else}
    <div class="log" bind:this={box} {onscroll} aria-live="polite">
      {#if hidden}<button class="btn small ghost earlier" type="button" onclick={revealEarlier}>Show {hidden} earlier lines</button>{/if}
      {#if error}<p class="ev error">Could not load the log: {error}</p>{/if}
      {#each shown as e (e.id)}
        {#if e.kind === 'tool_result'}
          <details class="ev tool_result">
            <summary><span class="mono">{firstLine(e.text) || '(empty)'}</span></summary>
            <pre>{e.text}</pre>
          </details>
        {:else if e.kind === 'thinking'}
          <details class="ev thinking">
            <summary>Thinking <span class="muted">— {firstLine(e.text).slice(0, 80)}</span></summary>
            <p>{e.text}</p>
          </details>
        {:else if e.kind === 'tool_call'}
          <div class="ev tool_call"><span class="arrow" aria-hidden="true">▸</span><span class="mono">{e.text}</span></div>
        {:else}
          <div class="ev {e.kind}"><p>{e.text}</p><time class="muted">{clock(e.createdAt)}</time></div>
        {/if}
      {:else}
        {#if !loading}<p class="ev system">Nothing logged yet.</p>{:else}<p class="ev system">Loading the log…</p>{/if}
      {/each}
      {#if run.status === 'running'}<div class="working" aria-label="Agent is working"><span></span><span></span><span></span></div>{/if}
    </div>
  {/if}

  {#if live && live.id === runId}
    <form class="send" onsubmit={send}>
      <input type="text" bind:value={message} placeholder="Message the agent" aria-label="Message the agent" enterkeyhint="send" />
      <button class="btn primary" type="submit" disabled={sending || !message.trim()} aria-label="Send"><Icon name="send" size={18} /></button>
    </form>
  {/if}
</div>

<style>
  .logtab {
    display: grid;
    gap: 10px;
  }
  .runs {
    display: flex;
    gap: 6px;
    overflow-x: auto;
    scrollbar-width: none;
  }
  .runs button {
    flex: none;
    height: 32px;
    padding: 0 10px;
    border-radius: 999px;
    border: 1px solid var(--line);
    background: var(--surface);
    font-size: 0.82rem;
    cursor: pointer;
  }
  .runs button.on {
    border-color: var(--ink);
  }
  .log {
    display: grid;
    gap: 2px;
    align-content: start;
    max-height: calc(100dvh - var(--topbar-h) - 300px);
    min-height: 280px;
    overflow-y: auto;
    padding: 10px;
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--r-l);
    font-size: 0.88rem;
  }
  .earlier {
    justify-self: center;
  }
  .ev {
    padding: 5px 6px;
    border-radius: var(--r-s);
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .ev p {
    white-space: pre-wrap;
  }
  .ev time {
    font-size: 0.72rem;
  }
  .ev.text {
    display: grid;
  }
  .ev.text time,
  .ev.error time {
    display: none;
  }
  .ev.system {
    color: var(--muted);
    font-size: 0.8rem;
    text-align: center;
    display: flex;
    justify-content: center;
    gap: 8px;
  }
  .ev.error {
    background: var(--bad-soft);
    color: var(--bad);
    font-family: var(--mono);
    font-size: 0.82rem;
  }
  .ev.user {
    justify-self: end;
    max-width: 85%;
    background: var(--primary);
    color: var(--primary-ink);
    padding: 8px 12px;
    border-radius: 14px 14px 4px 14px;
    margin: 4px 0;
    display: grid;
  }
  .ev.user time {
    color: inherit;
    opacity: 0.75;
    justify-self: end;
  }
  .ev.tool_call {
    display: flex;
    gap: 6px;
    color: var(--ink-2);
  }
  .arrow {
    color: var(--primary);
  }
  .ev.tool_result,
  .ev.thinking {
    color: var(--muted);
  }
  summary {
    cursor: pointer;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .ev.thinking summary {
    font-style: italic;
  }
  pre {
    margin: 6px 0 0;
    padding: 8px;
    background: var(--sunken);
    border-radius: var(--r-s);
    font-family: var(--mono);
    font-size: 0.78rem;
    white-space: pre-wrap;
    color: var(--ink-2);
  }
  .working {
    display: flex;
    gap: 4px;
    padding: 8px 6px;
  }
  .working span {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--lamp-working);
    animation: blink 1.2s infinite ease-in-out;
  }
  .working span:nth-child(2) {
    animation-delay: 0.2s;
  }
  .working span:nth-child(3) {
    animation-delay: 0.4s;
  }
  @keyframes blink {
    50% {
      opacity: 0.2;
    }
  }
  .send {
    display: flex;
    gap: 8px;
    position: sticky;
    bottom: calc(var(--nav-h) + var(--safe-b));
    margin: 0 -16px;
    padding: 8px 16px;
    background: var(--bg);
  }
  .send .btn {
    width: 48px;
    padding: 0;
    flex: none;
  }
</style>
