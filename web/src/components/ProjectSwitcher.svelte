<script lang="ts">
  import { api } from '../lib/api';
  import { app } from '../lib/app.svelte';
  import { href, router } from '../lib/router.svelte';
  import type { AgentKind, Project } from '../lib/types';
  import AgentPickers from './AgentPickers.svelte';
  import Sheet from './Sheet.svelte';

  let { open = $bindable(false), current }: { open?: boolean; current: Project | undefined } = $props();

  let adding = $state(false);
  let repo = $state('');
  let agent = $state<AgentKind>('claude');
  let model = $state('');
  let effort = $state('');
  let busy = $state(false);
  const validRepo = $derived(/^[\w.-]+\/[\w.-]+$/.test(repo.trim()));

  function startAdd() {
    const a = app.agents[0];
    agent = a?.kind ?? 'claude';
    model = a?.models[0]?.id ?? '';
    effort = a?.efforts[Math.floor((a?.efforts.length ?? 1) / 2)] ?? '';
    adding = true;
  }

  async function add(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    const p = await app.attempt(() => api.createProject({ repo: repo.trim(), agent, model, effort }), 'Project added');
    busy = false;
    if (!p) return;
    await app.load();
    repo = '';
    adding = false;
    open = false;
    router.go({ name: 'board', projectId: p.id });
  }
</script>

<Sheet bind:open title="Projects">
  <ul class="list">
    {#each app.state?.projects ?? [] as p (p.id)}
      <li>
        <a class={{ on: p.id === current?.id }} href={href({ name: 'board', projectId: p.id })} onclick={() => (open = false)}>
          <strong>{p.name}</strong>
          <span class="muted small">{[p.repo, p.model && app.modelLabel(p.agent, p.model), p.effort].filter(Boolean).join(' · ')}</span>
        </a>
      </li>
    {/each}
  </ul>
  {#if adding}
    <form class="add" onsubmit={add}>
      <label class="field">
        GitHub repository
        <input type="text" bind:value={repo} placeholder="owner/name" autocapitalize="off" spellcheck="false" />
      </label>
      <p class="muted small">Defaults for new tickets</p>
      <AgentPickers {agent} {model} {effort} onchange={(v) => ({ agent, model, effort } = v)} />
      <div class="row">
        <button class="btn" type="button" onclick={() => (adding = false)}>Cancel</button>
        <button class="btn primary grow" type="submit" disabled={busy || !validRepo}>Add project</button>
      </div>
    </form>
  {:else}
    <button class="btn" type="button" onclick={startAdd}>Add project</button>
  {/if}
</Sheet>

<style>
  .list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 6px;
  }
  .list a {
    display: grid;
    gap: 2px;
    padding: 12px 14px;
    border-radius: var(--r-m);
    border: 1px solid var(--line);
    color: var(--ink);
    text-decoration: none;
  }
  .list a.on {
    border-color: var(--primary);
    box-shadow: inset 0 0 0 1px var(--primary);
  }
  .add {
    display: grid;
    gap: 12px;
    padding-top: 4px;
    border-top: 1px solid var(--line);
  }
</style>
