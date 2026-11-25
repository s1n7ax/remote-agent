<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { href } from '../lib/router.svelte';
  import { LINK_LABEL, STATUSES, STATUS_LABEL, type LinkKind, type Ticket, type TicketLink } from '../lib/types';
  import Icon from './Icon.svelte';

  // Lists a ticket's links and adds or removes them. Candidates are the
  // project's other tickets (jobs excluded); the server refuses loops.
  let {
    projectId,
    selfId,
    links,
    kinds,
    onadd,
    onremove,
  }: {
    projectId: number;
    selfId?: number;
    links: TicketLink[];
    kinds: LinkKind[];
    onadd: (to: Ticket, kind: LinkKind) => Promise<void> | void;
    onremove: (l: TicketLink) => Promise<void> | void;
  } = $props();

  let candidates = $state<Ticket[]>([]);
  // svelte-ignore state_referenced_locally
  let kind = $state<LinkKind>(kinds[0]);
  let to = $state('');
  let busy = $state(false);

  onMount(async () => {
    try {
      const b = await api.board(projectId);
      candidates = STATUSES.flatMap((s) => b.columns[s] ?? [])
        .filter((t) => t.kind !== 'job' && t.id !== selfId)
        .sort((a, b) => (b.issue ?? b.id) - (a.issue ?? a.id));
    } catch {
      candidates = [];
    }
  });

  const offered = $derived(candidates.filter((t) => !links.some((l) => !l.deleted && l.ticketId === t.id && l.kind === kind)));

  const ref = (x: { issue?: number; id?: number; ticketId?: number }) => (x.issue ? `#${x.issue}` : `ticket ${x.ticketId ?? x.id}`);

  async function add() {
    const t = candidates.find((c) => c.id === +to);
    if (!t) return;
    busy = true;
    try {
      await onadd(t, kind);
      to = '';
    } finally {
      busy = false;
    }
  }

  async function remove(l: TicketLink) {
    busy = true;
    try {
      await onremove(l);
    } finally {
      busy = false;
    }
  }
</script>

<div class="links">
  {#each ['depends_on', 'blocks', 'related'] as const as k (k)}
    {@const list = links.filter((l) => l.kind === k)}
    {#if list.length}
      <div class="group">
        <h3>{LINK_LABEL[k]}</h3>
        <ul>
          {#each list as l (l.id)}
            <li style:--lamp="var(--lamp-{l.status ?? 'backlog'})">
              {#if l.deleted}
                <span class="target"><span class="ref">{ref(l)}</span> <span class="title">{l.title}</span></span>
                <span class="badge">deleted</span>
              {:else}
                <a class="target" href={href({ name: 'ticket', projectId, ticketId: l.ticketId, tab: 'log' })}>
                  <span class="ref">{ref(l)}</span> <span class="title">{l.title}</span>
                </a>
                <span class="badge status"><span class="lamp"></span>{l.status ? STATUS_LABEL[l.status] : ''}</span>
              {/if}
              <button class="icon-btn" type="button" aria-label="Remove link to {ref(l)}" disabled={busy} onclick={() => remove(l)}>
                <Icon name="x" size={18} />
              </button>
            </li>
          {/each}
        </ul>
      </div>
    {/if}
  {/each}
  {#if !links.length}<p class="muted small">No linked tickets.</p>{/if}
  <div class="add">
    <select bind:value={kind} aria-label="Link kind">
      {#each kinds as k (k)}<option value={k}>{LINK_LABEL[k]}</option>{/each}
    </select>
    <select bind:value={to} aria-label="Ticket to link">
      <option value="">Pick a ticket…</option>
      {#each offered as t (t.id)}<option value={String(t.id)}>{ref(t)} {t.title} ({STATUS_LABEL[t.status]})</option>{/each}
    </select>
    <button class="btn" type="button" disabled={busy || !to} onclick={add}>Add</button>
  </div>
</div>

<style>
  .links {
    display: grid;
    gap: 12px;
  }
  .group {
    display: grid;
    gap: 6px;
  }
  h3 {
    font-size: 0.82rem;
    font-weight: 600;
    color: var(--ink-2);
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 4px;
  }
  li {
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 44px;
    padding-left: 10px;
    border-left: 3px solid var(--lamp);
    background: var(--raised);
    border-radius: var(--r-m);
  }
  .target {
    flex: 1;
    min-width: 0;
    color: inherit;
    text-decoration: none;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .ref {
    font-weight: 600;
  }
  .status {
    gap: 6px;
  }
  .lamp {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--lamp);
  }
  .add {
    display: grid;
    grid-template-columns: minmax(0, 9rem) minmax(0, 1fr) auto;
    gap: 8px;
  }
</style>
