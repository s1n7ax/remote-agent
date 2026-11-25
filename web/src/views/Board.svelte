<script lang="ts">
  import { onMount } from 'svelte';
  import NewTicketSheet from '../components/NewTicketSheet.svelte';
  import Icon from '../components/Icon.svelte';
  import TicketCard from '../components/TicketCard.svelte';
  import { api, subscribe } from '../lib/api';
  import { app } from '../lib/app.svelte';
  import { errorText } from '../lib/format';
  import { STATUS_LABEL, STATUSES, type Board, type Project, type Status } from '../lib/types';

  let { project }: { project: Project } = $props();

  let board = $state<Board | null>(null);
  let error = $state<string | null>(null);
  let active = $state<Status>('backlog');
  let scroller = $state<HTMLElement>();
  let tabsEl = $state<HTMLElement>();
  let creating = $state(false);
  let chosen = false;
  let reload: ReturnType<typeof setTimeout> | undefined;

  async function load() {
    try {
      board = await api.board(project.id);
      error = null;
      if (!chosen) {
        chosen = true;
        const first = STATUSES.find((s) => s === 'needs_input' && board!.columns[s]?.length) ?? STATUSES.find((s) => board!.columns[s]?.length);
        if (first) requestAnimationFrame(() => show(first, 'instant'));
      }
    } catch (e) {
      error = errorText(e);
    }
  }

  $effect(() => {
    void project.id;
    chosen = false;
    board = null;
    load();
  });

  onMount(() =>
    subscribe((ev) => {
      if ((ev.type === 'board' || ev.type === 'ticket') && ev.projectId === project.id) {
        clearTimeout(reload);
        reload = setTimeout(load, 250);
      }
    }),
  );

  function show(s: Status, behavior: ScrollBehavior = 'smooth') {
    const col = scroller?.querySelector<HTMLElement>(`[data-status="${s}"]`);
    if (!col || !scroller) return;
    scroller.scrollTo({ left: col.offsetLeft - scroller.offsetLeft - 16, behavior });
    active = s;
  }

  function onscroll() {
    if (!scroller) return;
    const cols = [...scroller.querySelectorAll<HTMLElement>('[data-status]')];
    const x = scroller.scrollLeft + scroller.clientWidth * 0.35;
    let best = cols[0];
    for (const c of cols) if (c.offsetLeft - scroller.offsetLeft <= x) best = c;
    const s = best?.dataset.status as Status | undefined;
    if (s && s !== active) {
      active = s;
      tabsEl?.querySelector<HTMLElement>(`[data-tab="${s}"]`)?.scrollIntoView({ inline: 'nearest', block: 'nearest' });
    }
  }
</script>

<div class="board-page">
  <nav class="tabs" bind:this={tabsEl} aria-label="Columns">
    {#each STATUSES as s (s)}
      {@const n = board?.columns[s]?.length ?? 0}
      <button class={{ on: active === s, hot: s === 'needs_input' && n > 0 }}
        type="button"
        data-tab={s}
        style:--lamp="var(--lamp-{s})"
        aria-current={active === s}
        onclick={() => show(s)}>
        <span class="lamp"></span>{STATUS_LABEL[s]}<span class="n">{n}</span>
      </button>
    {/each}
  </nav>

  {#if error}
    <div class="empty">
      <p>Could not load the board: {error}</p>
      <button class="btn" type="button" onclick={load}>Try again</button>
    </div>
  {:else}
    <div class="columns" bind:this={scroller} {onscroll}>
      {#each STATUSES as s (s)}
        {@const list = board?.columns[s] ?? []}
        <section class="col" data-status={s} style:--lamp="var(--lamp-{s})" aria-label={STATUS_LABEL[s]}>
          <header><span class="lamp"></span><h2>{STATUS_LABEL[s]}</h2><span class="muted small">{list.length}</span></header>
          <div class="list">
            {#each list as t (t.id)}
              <TicketCard {t} />
            {:else}
              <p class="none">{board ? 'Nothing here' : 'Loading…'}</p>
            {/each}
          </div>
        </section>
      {/each}
    </div>
  {/if}

  <button class="fab" type="button" onclick={() => (creating = true)}><Icon name="plus" /> New ticket</button>
  <NewTicketSheet bind:open={creating} {project} oncreated={load} />
</div>

<style>
  .board-page {
    display: flex;
    flex-direction: column;
    height: calc(100dvh - var(--topbar-h));
  }
  .tabs {
    display: flex;
    gap: 4px;
    padding: 8px 16px;
    overflow-x: auto;
    scrollbar-width: none;
    flex: none;
    border-bottom: 1px solid var(--line);
    background: var(--bg);
  }
  .tabs::-webkit-scrollbar {
    display: none;
  }
  .tabs button {
    flex: none;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 34px;
    padding: 0 12px;
    border: 0;
    border-radius: 999px;
    background: transparent;
    color: var(--ink-2);
    font-size: 0.86rem;
    font-weight: 500;
    cursor: pointer;
  }
  .tabs button.on {
    background: var(--surface);
    color: var(--ink);
    box-shadow: var(--shadow-card);
  }
  .tabs .n {
    color: var(--muted);
    font-size: 0.8rem;
  }
  .tabs button.hot .lamp {
    box-shadow: 0 0 0 3px color-mix(in srgb, var(--lamp) 30%, transparent);
  }
  .tabs button.hot .n {
    color: var(--attention-ink);
    font-weight: 600;
  }
  .columns {
    flex: 1;
    display: flex;
    gap: 12px;
    padding: 12px 16px calc(var(--nav-h) + var(--safe-b) + 76px);
    overflow-x: auto;
    scroll-snap-type: x mandatory;
    scroll-padding-inline: 16px;
    overscroll-behavior-x: contain;
    scrollbar-width: none;
  }
  .columns::-webkit-scrollbar {
    display: none;
  }
  .col {
    flex: none;
    width: calc(100vw - 48px);
    max-width: 420px;
    scroll-snap-align: start;
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-height: 0;
  }
  .col header {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 0 4px;
  }
  .col h2 {
    font-size: 0.9rem;
    flex: 1;
  }
  .list {
    display: grid;
    gap: 8px;
    align-content: start;
    overflow-y: auto;
    padding: 1px;
  }
  .none {
    padding: 20px 8px;
    border: 1px dashed var(--line-strong);
    border-radius: var(--r-m);
    text-align: center;
    color: var(--muted);
    font-size: 0.86rem;
  }
  .fab {
    position: fixed;
    right: 16px;
    bottom: calc(var(--nav-h) + var(--safe-b) + 16px);
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 52px;
    padding: 0 20px 0 16px;
    border-radius: 26px;
    border: 0;
    background: var(--primary);
    color: var(--primary-ink);
    font-weight: 600;
    box-shadow: 0 6px 20px color-mix(in srgb, var(--primary) 40%, transparent);
    cursor: pointer;
    z-index: 5;
  }
  @media (min-width: 900px) {
    .tabs {
      display: none;
    }
    .columns {
      scroll-snap-type: none;
      padding-bottom: 24px;
    }
    .col {
      width: 292px;
    }
    .fab {
      bottom: 24px;
      right: 24px;
    }
  }
</style>
