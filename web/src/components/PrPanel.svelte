<script lang="ts">
  import { api } from '../lib/api';
  import { app } from '../lib/app.svelte';
  import { href } from '../lib/router.svelte';
  import type { Check, PullRequest } from '../lib/types';
  import CiBadge from './CiBadge.svelte';
  import Sheet from './Sheet.svelte';

  let {
    pr,
    projectId,
    ticketId,
    onmerged,
  }: { pr: PullRequest; projectId: number; ticketId?: number; onmerged: () => void } = $props();

  let confirming = $state(false);
  let merging = $state(false);
  const checks = $derived(pr.checks ?? []);
  const open = $derived(pr.state === 'open' && !pr.merged);
  const conflicted = $derived(pr.mergeableState === 'dirty');
  const canMerge = $derived(open && !pr.draft && !conflicted);
  const why = $derived(
    pr.merged
      ? 'Merged'
      : pr.state === 'closed'
        ? 'Closed'
        : pr.draft
          ? 'Draft: the agent marks it ready after verify'
          : conflicted
            ? 'Has merge conflicts'
            : pr.mergeableState === 'blocked'
              ? 'GitHub reports it blocked (required reviews or checks)'
              : '',
  );

  async function merge() {
    merging = true;
    const res = await app.attempt(() => api.merge(projectId, pr.number, pr.headSha));
    merging = false;
    if (!res) return;
    confirming = false;
    app.notify(`Merged #${pr.number}`);
    onmerged();
  }

  function checkCls(c: Check) {
    if (c.status !== 'completed') return 'pending';
    return c.conclusion === 'success' || c.conclusion === 'skipped' || c.conclusion === 'neutral' ? 'ok' : 'bad';
  }
</script>

<article class="pr">
  <div class="top">
    <div class="grow">
      <a class="title" href={pr.url} target="_blank" rel="noopener noreferrer">#{pr.number} {pr.title}</a>
      <p class="muted small">
        <span class="mono">{pr.branch}</span>
        {#if ticketId}· <a href={href({ name: 'ticket', projectId, ticketId, tab: 'pr' })}>ticket</a>{/if}
      </p>
    </div>
    <CiBadge ci={pr.ci} />
  </div>
  <div class="row wrap">
    {#if pr.draft}<span class="badge">Draft</span>{/if}
    {#if pr.merged}<span class="badge ok">Merged</span>{:else if pr.state === 'closed'}<span class="badge">Closed</span>{/if}
    {#if conflicted}<span class="badge bad">Conflicts</span>{/if}
  </div>
  {#if checks.length}
    <ul class="checks">
      {#each checks as c (c.id || c.name)}
        {@const cls = checkCls(c)}
        <li>
          <span class={['mark', cls]} aria-hidden="true">{cls === 'ok' ? '✓' : cls === 'bad' ? '✕' : '●'}</span>
          {#if c.url}<a href={c.url} target="_blank" rel="noopener noreferrer" class="grow">{c.name}</a>{:else}<span class="grow">{c.name}</span>{/if}
          <span class="muted small">{c.status === 'completed' ? c.conclusion : c.status.replace('_', ' ')}</span>
        </li>
      {/each}
    </ul>
  {/if}
  {#if open}
    <div class="row">
      <button class="btn primary" type="button" disabled={!canMerge} onclick={() => (confirming = true)}>Merge…</button>
      {#if why}<span class="muted small">{why}</span>{/if}
    </div>
  {/if}
</article>

<Sheet bind:open={confirming} title="Merge #{pr.number}?">
  <p><strong>{pr.title}</strong></p>
  <p class="muted">Merges into the default branch using the repository’s merge method. Only you can merge; agents cannot.</p>
  {#if pr.ci !== 'success'}<p class="warn">CI is not green ({pr.ci}).</p>{/if}
  {#snippet footer()}
    <button class="btn grow" type="button" onclick={() => (confirming = false)}>Cancel</button>
    <button class="btn primary grow" type="button" disabled={merging} onclick={merge}>{merging ? 'Merging…' : 'Merge pull request'}</button>
  {/snippet}
</Sheet>

<style>
  .pr {
    display: grid;
    gap: 10px;
    padding: 14px;
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--r-l);
  }
  .top {
    display: flex;
    gap: 10px;
    align-items: flex-start;
  }
  .title {
    font-weight: 600;
    color: var(--ink);
    text-decoration: none;
    overflow-wrap: anywhere;
  }
  .row:empty {
    display: none;
  }
  .checks {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    border-top: 1px solid var(--line);
  }
  .checks li {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 0;
    border-bottom: 1px solid var(--line);
    font-size: 0.9rem;
  }
  .checks a {
    color: inherit;
  }
  .mark {
    width: 20px;
    text-align: center;
    font-size: 0.8rem;
  }
  .mark.ok {
    color: var(--ok);
  }
  .mark.bad {
    color: var(--bad);
  }
  .mark.pending {
    color: var(--pending);
  }
  .warn {
    color: var(--bad);
  }
</style>
