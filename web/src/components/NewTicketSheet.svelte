<script lang="ts">
  import { api } from '../lib/api';
  import { app } from '../lib/app.svelte';
  import { router } from '../lib/router.svelte';
  import type { AgentKind, Project } from '../lib/types';
  import AgentPickers from './AgentPickers.svelte';
  import Sheet from './Sheet.svelte';

  let { open = $bindable(false), project, oncreated }: { open?: boolean; project: Project; oncreated: () => void } = $props();

  let idea = $state('');
  // Project defaults, overridable by the pickers.
  let agent = $derived<AgentKind>(project.agent);
  let model = $derived(project.model);
  let effort = $derived(project.effort);
  let prototypes = $state(false);
  let busy = $state(false);

  async function create(start: boolean) {
    if (!idea.trim()) return;
    busy = true;
    const t = await app.attempt(
      () => api.createTicket(project.id, { idea: idea.trim(), agent, model, effort, prototypes, start }),
      start ? 'Ticket created and started' : 'Ticket created in backlog',
    );
    busy = false;
    if (!t) return;
    idea = '';
    prototypes = false;
    ({ agent, model, effort } = project);
    open = false;
    oncreated();
    if (start) router.go({ name: 'ticket', projectId: project.id, ticketId: t.id, tab: 'log' });
  }
</script>

<Sheet bind:open title="New ticket">
  <label class="field">
    Rough idea
    <textarea bind:value={idea} rows="5" placeholder="What should change, and why? The agent will ask what it needs."></textarea>
  </label>
  <AgentPickers {agent} {model} {effort} onchange={(v) => ({ agent, model, effort } = v)} />
  <label class="toggle">
    <input type="checkbox" bind:checked={prototypes} />
    <span>
      <strong>Prototypes</strong>
      <span class="muted small">Build 2–3 options with screenshots and a live preview to pick from.</span>
    </span>
  </label>
  {#snippet footer()}
    <button class="btn grow" type="button" disabled={busy || !idea.trim()} onclick={() => create(false)}>Create</button>
    <button class="btn primary grow" type="button" disabled={busy || !idea.trim()} onclick={() => create(true)}>Create &amp; start</button>
  {/snippet}
</Sheet>

<style>
  .toggle {
    display: flex;
    gap: 12px;
    align-items: flex-start;
    padding: 12px;
    border: 1px solid var(--line);
    border-radius: var(--r-m);
    cursor: pointer;
  }
  .toggle input {
    width: 20px;
    height: 20px;
    margin: 2px 0 0;
    accent-color: var(--primary);
  }
  .toggle > span {
    display: grid;
    gap: 2px;
  }
</style>
