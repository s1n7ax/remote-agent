<script lang="ts">
  import { app } from '../lib/app.svelte';
  import type { AgentKind } from '../lib/types';
  import Chips from './Chips.svelte';

  let {
    agent,
    model,
    effort,
    onchange,
    disabled = false,
  }: {
    agent: AgentKind;
    model: string;
    effort: string;
    onchange: (v: { agent: AgentKind; model: string; effort: string }) => void;
    disabled?: boolean;
  } = $props();

  const agents = $derived(app.agents);
  const info = $derived(agents.find((a) => a.kind === agent));

  function setAgent(kind: AgentKind) {
    const next = agents.find((a) => a.kind === kind);
    if (!next) return;
    const m = next.models.some((x) => x.id === model) ? model : (next.models[0]?.id ?? '');
    const e = next.efforts.includes(effort) ? effort : (next.efforts[Math.floor(next.efforts.length / 2)] ?? '');
    onchange({ agent: kind, model: m, effort: e });
  }
</script>

<div class="pickers">
  <div class="line">
    <span class="k">Agent</span>
    <Chips label="Agent" {disabled} options={agents.map((a) => ({ value: a.kind, label: a.label }))} value={agent} onchange={setAgent} />
  </div>
  <div class="line">
    <span class="k">Model</span>
    <Chips
      label="Model"
      {disabled}
      options={(info?.models ?? []).map((m) => ({ value: m.id, label: m.label }))}
      value={model}
      onchange={(m) => onchange({ agent, model: m, effort })} />
  </div>
  <div class="line">
    <span class="k">Effort</span>
    <Chips
      label="Effort"
      {disabled}
      options={(info?.efforts ?? []).map((e) => ({ value: e, label: e }))}
      value={effort}
      onchange={(e) => onchange({ agent, model, effort: e })} />
  </div>
</div>

<style>
  .pickers {
    display: grid;
    gap: 8px;
  }
  .line {
    display: grid;
    grid-template-columns: 52px minmax(0, 1fr);
    align-items: center;
    gap: 8px;
  }
  .k {
    font-size: 0.8rem;
    color: var(--muted);
    font-weight: 500;
  }
  /* On phones the chip values speak for themselves; groups keep their aria-labels. */
  @media (max-width: 440px) {
    .line {
      grid-template-columns: minmax(0, 1fr);
    }
    .k {
      display: none;
    }
  }
</style>
