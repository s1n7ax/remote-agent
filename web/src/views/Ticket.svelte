<script lang="ts">
  import { onMount } from 'svelte';
  import AgentPickers from '../components/AgentPickers.svelte';
  import Icon from '../components/Icon.svelte';
  import ImagesPanel from '../components/ImagesPanel.svelte';
  import LinkEditor from '../components/LinkEditor.svelte';
  import LogTab from '../components/LogTab.svelte';
  import Markdown from '../components/Markdown.svelte';
  import PrPanel from '../components/PrPanel.svelte';
  import ProofTab from '../components/ProofTab.svelte';
  import Prototypes from '../components/Prototypes.svelte';
  import QuestionsTab from '../components/QuestionsTab.svelte';
  import Sheet from '../components/Sheet.svelte';
  import { api, subscribe } from '../lib/api';
  import { app } from '../lib/app.svelte';
  import { clock, errorText, relative, ticketRef, toLocalInput, usd } from '../lib/format';
  import { confirmStart } from '../lib/images';
  import { progress } from '../lib/questions';
  import { href, router } from '../lib/router.svelte';
  import { STATUS_LABEL, type LinkKind, type Project, type PullRequest, type Round, type Ticket, type TicketDetail, type TicketLink } from '../lib/types';

  let { project, ticketId, tab }: { project: Project; ticketId: number; tab: string } = $props();

  let detail = $state<TicketDetail | null>(null);
  let error = $state<string | null>(null);
  let busy = $state(false);
  let scheduling = $state(false);
  let when = $state('');

  async function load() {
    try {
      const d = await api.ticket(ticketId);
      // Go encodes empty slices as null.
      d.rounds ??= [];
      d.runs ??= [];
      d.prototypes ??= [];
      d.links ??= [];
      d.images ??= [];
      detail = d;
      error = null;
    } catch (e) {
      error = errorText(e);
    }
  }

  $effect(() => {
    void ticketId;
    detail = null;
    load();
  });

  onMount(() =>
    subscribe((ev) => {
      if (ev.type === 'ticket' && ev.ticketId === ticketId && detail) {
        const changed = ev.data.status !== detail.ticket.status || ev.data.stage !== detail.ticket.stage || ev.data.pr !== detail.ticket.pr;
        detail.ticket = ev.data;
        if (changed) load();
      }
      // A linked ticket changed: its title, status or the links themselves.
      if (detail && ((ev.type === 'links' && ev.ticketId === ticketId) || (ev.type === 'ticket' && detail.links.some((l) => l.ticketId === ev.ticketId)))) {
        refreshLinks();
      }
    }),
  );

  async function refreshLinks() {
    try {
      const d = await api.ticket(ticketId);
      if (detail) detail.links = d.links ?? [];
    } catch {
      // the next event or reload tries again
    }
  }

  async function addLink(to: Ticket, kind: LinkKind) {
    const res = await app.attempt(() => api.addLink(ticketId, to.id, kind), 'Link added');
    if (res && detail) detail.links = res.links ?? [];
  }

  async function removeLink(l: TicketLink) {
    const res = await app.attempt(() => api.removeLink(ticketId, l.id), 'Link removed');
    if (res && detail) detail.links = res.links ?? [];
  }

  const unmerged = $derived(detail?.links.filter((l) => l.kind === 'depends_on' && (l.deleted || l.status !== 'done')) ?? []);

  const t = $derived(detail?.ticket);
  const live = $derived(detail?.runs.filter((r) => ['queued', 'running', 'paused'].includes(r.status)).at(-1));
  const openRound = $derived(detail?.rounds.find((r) => !r.completedAt));
  const openProg = $derived(openRound ? progress(openRound.tree, openRound.answers) : null);

  const tabs = $derived([
    { id: 'questions', label: 'Questions', count: openProg ? openProg.total - openProg.answered : 0 },
    { id: 'log', label: 'Log', count: 0 },
    { id: 'proof', label: 'Proof', count: 0 },
    { id: 'requirement', label: 'Requirement', count: 0 },
    { id: 'pr', label: 'PR', count: 0 },
  ]);

  // The ticket detail carries only the PR number; CI and mergeability come from the project's GitHub view.
  let pr = $state<PullRequest | null>(null);
  let prError = $state<string | null>(null);
  async function loadPr() {
    const n = detail?.ticket.pr;
    if (!n) return;
    try {
      const gh = await api.github(project.id);
      pr = gh.pulls.find((p) => p.number === n) ?? null;
      prError = pr ? null : `PR #${n} is not open on GitHub any more.`;
    } catch (e) {
      prError = errorText(e);
    }
  }
  $effect(() => {
    if (tab === 'pr' && detail?.ticket.pr) loadPr();
  });

  async function remove() {
    if (!confirm('Delete this ticket? Its run stops and its worktree is removed; the GitHub issue and branch stay.')) return;
    const ok = await app.attempt(() => api.deleteTicket(ticketId), 'Ticket deleted');
    if (ok) router.go({ name: 'board', projectId: project.id });
  }

  $effect(() => {
    if (!tabs.some((x) => x.id === tab)) router.go({ name: 'ticket', projectId: project.id, ticketId, tab: 'questions' }, true);
  });

  async function act(fn: () => Promise<Ticket>, ok: string) {
    busy = true;
    const res = await app.attempt(fn, ok);
    busy = false;
    if (res && detail) {
      detail.ticket = res;
      load();
    }
  }

  async function patch(v: { agent: Ticket['agent']; model: string; effort: string }) {
    if (!detail) return;
    const prev = detail.ticket;
    detail.ticket = { ...prev, ...v };
    const res = await app.attempt(() => api.patchTicket(ticketId, v));
    if (detail) detail.ticket = res ?? prev;
  }

  // Starting a ticket whose model cannot see its images needs a confirm.
  function start() {
    if (!t || !detail) return;
    if (!confirmStart(app.state, t.agent, t.model, detail.images.length)) return;
    act(() => api.start(ticketId), 'Started');
  }

  function openSchedule() {
    when = toLocalInput(t?.startAt);
    scheduling = true;
  }

  async function schedule(clear = false) {
    const startAt = new Date(when).toISOString();
    await act(
      () => api.patchTicket(ticketId, clear ? { clearStartAt: true } : { startAt }),
      clear ? 'Schedule cleared' : `Starts ${relative(startAt)}`,
    );
    scheduling = false;
  }

  function onsaved(r: Round, tk: Ticket) {
    if (!detail) return;
    const i = detail.rounds.findIndex((x) => x.n === r.n);
    if (i >= 0) detail.rounds[i] = r;
    detail.ticket = tk;
  }
</script>

{#if error}
  <div class="page"><div class="empty"><p>Could not load ticket #{ticketId}: {error}</p><button class="btn" type="button" onclick={load}>Try again</button></div></div>
{:else if !t || !detail}
  <div class="page"><p class="muted">Loading…</p></div>
{:else}
  <div class="page ticket" style:--lamp="var(--lamp-{t.status})">
    <header class="head">
      <a class="back" href={href({ name: 'board', projectId: project.id })}><Icon name="back" size={18} /> Board</a>
      <h1>{t.title}</h1>
      <div class="row wrap status">
        <span class="pill"><span class="lamp"></span>{STATUS_LABEL[t.status]}</span>
        <span class="badge">{t.kind === 'job' ? 'job' : `stage: ${t.stage}`}</span>
        {#if t.issue}<a class="badge" href="https://github.com/{project.repo}/issues/{t.issue}" target="_blank" rel="noopener noreferrer">issue #{t.issue}</a>{/if}
        {#if t.agent === 'pi'}<span class="badge">{usd(t.spendUsd)}{t.spendCapUsd > 0 ? ` of ${usd(t.spendCapUsd)}` : ' spent'}</span>{/if}
      </div>
      {#if t.waitUntil}
        <p class="note">Waiting until {clock(t.waitUntil)} ({relative(t.waitUntil)}). It resumes by itself.</p>
      {:else if t.startAt && t.status === 'backlog'}
        <p class="note">Scheduled to start {clock(t.startAt)} ({relative(t.startAt)}).</p>
      {/if}
      {#if t.statusNote}<p class={['note', { bad: t.status === 'failed' }]}>{t.statusNote}</p>{/if}
    </header>

    <section class="panel controls">
      <AgentPickers agent={t.agent} model={t.model} effort={t.effort} images={detail.images.length} onchange={patch} />
      <div class="actions">
        {#if t.status === 'backlog'}
          <button class="btn primary" type="button" disabled={busy} onclick={start}><Icon name="play" size={16} /> Start</button>
          <button class="btn" type="button" disabled={busy} onclick={openSchedule}><Icon name="calendar" size={16} /> {t.startAt ? 'Reschedule' : 'Schedule'}</button>
        {/if}
        {#if live?.status === 'running'}
          <button class="btn" type="button" disabled={busy} onclick={() => act(() => api.pause(ticketId), 'Paused')}><Icon name="pause" size={16} /> Pause</button>
        {/if}
        {#if live?.status === 'paused'}
          <button class="btn primary" type="button" disabled={busy} onclick={() => act(() => api.resume(ticketId), 'Resumed')}><Icon name="play" size={16} /> Resume</button>
        {/if}
        {#if live || ['ready', 'working', 'waiting'].includes(t.status)}
          <button class="btn danger" type="button" disabled={busy} onclick={() => act(() => api.stop(ticketId), 'Stopped')}><Icon name="stop" size={16} /> Stop</button>
        {/if}
        {#if ['backlog', 'done', 'failed'].includes(t.status)}
          <button class="btn ghost danger" type="button" disabled={busy} onclick={remove}>Delete</button>
        {/if}
        {#if t.status === 'failed'}
          <button class="btn primary" type="button" disabled={busy} onclick={() => act(() => api.retry(ticketId), 'Retrying')}><Icon name="retry" size={16} /> Retry</button>
        {/if}
      </div>
    </section>

    {#if t.kind !== 'job'}
      <section class="panel" aria-label="Linked tickets">
        <h2>Links</h2>
        <LinkEditor projectId={project.id} selfId={t.id} links={detail.links} kinds={['depends_on', 'blocks', 'related']} onadd={addLink} onremove={removeLink} />
      </section>
      <ImagesPanel {ticketId} bind:images={detail.images} />
    {/if}

    <nav class="tabs" aria-label="Ticket sections">
      {#each tabs as x (x.id)}
        <a class={{ on: tab === x.id }} href={href({ name: 'ticket', projectId: project.id, ticketId, tab: x.id })} aria-current={tab === x.id ? 'page' : undefined}>
          {x.label}{#if x.count}<span class="count">{x.count}</span>{/if}
        </a>
      {/each}
    </nav>

    {#if tab === 'questions'}
      {#if detail.prototypes?.length}
        <Prototypes {ticketId} prototypes={detail.prototypes} preview={detail.preview} onchange={load} />
      {/if}
      <QuestionsTab rounds={detail.rounds} {onsaved} />
    {:else if tab === 'log'}
      <LogTab {ticketId} runs={detail.runs} />
    {:else if tab === 'proof'}
      <ProofTab proof={detail.proof} />
    {:else if tab === 'requirement'}
      <section class="panel">
        <h2>Idea</h2>
        <p class="idea">{t.idea}</p>
      </section>
      <section class="panel">
        <h2>Requirement</h2>
        {#if t.requirement}<Markdown src={t.requirement} />{:else}<p class="muted">Written after round 1 is answered.</p>{/if}
      </section>
      <section class="panel">
        <h2>Decisions</h2>
        {#if t.decisions}<Markdown src={t.decisions} />{:else}<p class="muted">Written during the design stage.</p>{/if}
      </section>
    {:else if tab === 'pr'}
      {#if !t.pr}
        <div class="empty"><p>No pull request yet.</p><p class="small">The build stage opens a draft PR.</p></div>
      {:else if pr}
        {#if unmerged.length}
          <p class="note bad">Not merged yet: {unmerged.map(ticketRef).join(', ')}, which this ticket depends on. Merging is still your call.</p>
        {/if}
        <PrPanel {pr} projectId={project.id} onmerged={() => (load(), loadPr())} />
      {:else if prError}
        <div class="empty">
          <p>{prError}</p>
          <a class="btn" href="https://github.com/{project.repo}/pull/{t.pr}" target="_blank" rel="noopener noreferrer">Open PR #{t.pr} on GitHub</a>
        </div>
      {:else}
        <p class="muted">Loading PR #{t.pr}…</p>
      {/if}
    {/if}
  </div>

  <Sheet bind:open={scheduling} title="Schedule start">
    <label class="field">Start at <input type="datetime-local" bind:value={when} /></label>
    {#snippet footer()}
      {#if t?.startAt}<button class="btn" type="button" onclick={() => schedule(true)}>Clear</button>{/if}
      <button class="btn primary grow" type="button" disabled={!when || busy} onclick={() => schedule()}>Schedule</button>
    {/snippet}
  </Sheet>
{/if}

<style>
  .head {
    display: grid;
    gap: 8px;
  }
  .back {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--ink-2);
    text-decoration: none;
    font-size: 0.88rem;
    justify-self: start;
    min-height: 32px;
  }
  .pill {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 26px;
    padding: 0 10px;
    border-radius: 999px;
    background: color-mix(in srgb, var(--lamp) 16%, var(--surface));
    color: var(--ink);
    font-weight: 600;
    font-size: 0.82rem;
  }
  a.badge {
    text-decoration: none;
  }
  .note {
    font-size: 0.9rem;
    color: var(--ink-2);
  }
  .note.bad {
    color: var(--bad);
  }
  .controls {
    gap: 14px;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  .actions:empty {
    display: none;
  }
  .tabs {
    display: flex;
    gap: 2px;
    overflow-x: auto;
    scrollbar-width: none;
    border-bottom: 1px solid var(--line);
    position: sticky;
    top: var(--topbar-h);
    background: var(--bg);
    z-index: 3;
    margin: 0 -16px;
    padding: 0 12px;
  }
  .tabs a {
    flex: none;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 44px;
    padding: 0 10px;
    color: var(--ink-2);
    text-decoration: none;
    font-weight: 500;
    font-size: 0.9rem;
    border-bottom: 2px solid transparent;
    margin-bottom: -1px;
  }
  .tabs a.on {
    color: var(--ink);
    border-bottom-color: var(--ink);
  }
  .count {
    min-width: 20px;
    height: 20px;
    padding: 0 6px;
    border-radius: 10px;
    background: var(--attention);
    color: #1d1400;
    font-size: 0.74rem;
    font-weight: 600;
    display: inline-grid;
    place-items: center;
  }
  .idea {
    white-space: pre-wrap;
  }
</style>
