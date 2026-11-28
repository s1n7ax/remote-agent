<script lang="ts">
  import { app } from '../lib/app.svelte';
  import { clock, relative } from '../lib/format';
  import { href } from '../lib/router.svelte';
  import type { Ticket } from '../lib/types';

  let { t }: { t: Ticket } = $props();
  const attention = $derived(t.status === 'needs_input');
</script>

<a class={['card', { attention }]} href={href({ name: 'ticket', projectId: t.projectId, ticketId: t.id, tab: attention ? 'questions' : 'log' })}>
  {#if attention}
    <div class="ask">
      <span>Waiting for your answers</span>
    </div>
  {/if}
  <h3>{t.title}</h3>
  <div class="meta">
    <span class="badge">{t.kind === 'job' ? 'job' : t.stage}</span>
    <span class="badge">{app.modelLabel(t.agent, t.model)} · {t.effort}</span>
    {#if t.agent !== 'claude'}<span class="badge">{app.agent(t.agent)?.label ?? t.agent}</span>{/if}
    {#if t.pr}<span class="badge">PR #{t.pr}</span>{/if}
    {#if t.agent === 'pi' && t.spendUsd > 0}<span class="badge">${t.spendUsd.toFixed(2)}</span>{/if}
  </div>
  {#if t.waitUntil}
    <p class="note">Resumes {relative(t.waitUntil)} <span class="muted">({clock(t.waitUntil)})</span></p>
  {:else if t.startAt && t.status === 'backlog'}
    <p class="note">Starts {relative(t.startAt)} <span class="muted">({clock(t.startAt)})</span></p>
  {:else if t.statusNote && t.status === 'failed'}
    <p class="note bad">{t.statusNote}</p>
  {:else if t.statusNote?.startsWith('waiting on ') && t.status === 'ready'}
    <p class="note">{t.statusNote}</p>
  {/if}
</a>

<style>
  .card {
    display: grid;
    gap: 8px;
    padding: 12px;
    background: var(--raised);
    border-radius: var(--r-m);
    box-shadow: var(--shadow-card);
    color: inherit;
    text-decoration: none;
    border-left: 3px solid var(--lamp);
  }
  .card.attention {
    background: color-mix(in srgb, var(--attention-soft) 70%, var(--raised));
    box-shadow:
      0 0 0 1px color-mix(in srgb, var(--attention) 55%, transparent),
      var(--shadow-card);
  }
  .ask {
    display: flex;
    justify-content: space-between;
    font-size: 0.78rem;
    font-weight: 600;
    color: var(--attention-ink);
  }
  h3 {
    font-size: 0.95rem;
    font-weight: 500;
    line-height: 1.3;
  }
  .meta {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }
  .note {
    font-size: 0.82rem;
    color: var(--ink-2);
  }
  .note.bad {
    color: var(--bad);
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
</style>
