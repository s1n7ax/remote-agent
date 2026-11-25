<script lang="ts">
  import AgentPickers from '../components/AgentPickers.svelte';
  import Sheet from '../components/Sheet.svelte';
  import { api } from '../lib/api';
  import { app } from '../lib/app.svelte';
  import { clock, errorText, relative } from '../lib/format';
  import type { Project, Schedule, ScheduleInput } from '../lib/types';

  let { project }: { project: Project } = $props();

  const presets = [
    { label: 'Daily 9:00', cron: '0 9 * * *' },
    { label: 'Hourly', cron: '0 * * * *' },
    { label: 'Weekdays 8:00', cron: '0 8 * * 1-5' },
    { label: 'Mondays 8:00', cron: '0 8 * * 1' },
    { label: 'Nightly 2:00', cron: '0 2 * * *' },
  ];

  let list = $state<Schedule[] | null>(null);
  let error = $state<string | null>(null);
  let editing = $state<Schedule | null>(null);
  let form = $state<ScheduleInput>(blank());
  let open = $state(false);
  let busy = $state(false);

  function blank(): ScheduleInput {
    return { name: '', cron: '0 9 * * *', prompt: '', agent: project.agent, model: project.model, effort: project.effort, enabled: true };
  }

  async function load() {
    try {
      list = (await api.schedules(project.id)) ?? [];
      error = null;
    } catch (e) {
      error = errorText(e);
    }
  }

  $effect(() => {
    void project.id;
    list = null;
    load();
  });

  function edit(s: Schedule | null) {
    editing = s;
    form = s ? { name: s.name, cron: s.cron, prompt: s.prompt, agent: s.agent, model: s.model, effort: s.effort, enabled: s.enabled } : blank();
    open = true;
  }

  async function save() {
    busy = true;
    const ok = await app.attempt(
      () => (editing ? api.updateSchedule(editing.id, form) : api.createSchedule(project.id, form)),
      editing ? 'Schedule saved' : 'Schedule created',
    );
    busy = false;
    if (ok) {
      open = false;
      load();
    }
  }

  async function remove() {
    if (!editing || !confirm(`Delete “${editing.name}”?`)) return;
    const id = editing.id;
    const ok = await app.attempt(async () => {
      await api.deleteSchedule(id);
      return true;
    }, 'Schedule deleted');
    if (ok) {
      open = false;
      load();
    }
  }

  async function toggle(s: Schedule) {
    const res = await app.attempt(() => api.updateSchedule(s.id, { enabled: !s.enabled }));
    if (res) s.enabled = res.enabled;
  }

  const presetLabel = (cron: string) => presets.find((p) => p.cron === cron)?.label;
  const valid = $derived(form.name.trim() && form.prompt.trim() && form.cron.trim().split(/\s+/).length === 5);
</script>

<div class="page">
  <div class="row spread">
    <h1>Scheduled jobs</h1>
    <button class="btn primary small" type="button" onclick={() => edit(null)}>New job</button>
  </div>
  {#if error}
    <div class="empty"><p>Could not load schedules: {error}</p></div>
  {:else if !list}
    <p class="muted">Loading…</p>
  {:else}
    {#each list as s (s.id)}
      <article class={['panel job', { off: !s.enabled }]}>
        <div class="row">
          <button type="button" class="grow name" onclick={() => edit(s)}>
            <strong>{s.name}</strong>
            <span class="muted small"><span class="mono">{s.cron}</span>{#if presetLabel(s.cron)} ({presetLabel(s.cron)}){/if}</span>
          </button>
          <label class="switch" aria-label="Enabled">
            <input type="checkbox" checked={s.enabled} onchange={() => toggle(s)} />
            <span></span>
          </label>
        </div>
        <p class="prompt">{s.prompt}</p>
        <p class="muted small">
          {app.modelLabel(s.agent, s.model)} · {s.effort}
          {#if s.enabled && s.nextRunAt} · next {relative(s.nextRunAt)}{/if}
          {#if s.lastRunAt} · last {clock(s.lastRunAt)}{/if}
        </p>
      </article>
    {:else}
      <div class="empty">
        <p>No scheduled jobs.</p>
        <p class="small">A job runs its prompt on a cron schedule and shows up on the board.</p>
      </div>
    {/each}
  {/if}
</div>

<Sheet bind:open title={editing ? 'Edit job' : 'New job'}>
  <label class="field">Name <input type="text" bind:value={form.name} placeholder="Fix merge conflicts" /></label>
  <div class="field">
    <label class="field">Cron <input type="text" class="mono" bind:value={form.cron} autocapitalize="off" spellcheck="false" /></label>
    <div class="presets">
      {#each presets as p (p.cron)}
        <button class={['btn small', { primary: form.cron === p.cron }]} type="button" onclick={() => (form.cron = p.cron)}>{p.label}</button>
      {/each}
    </div>
  </div>
  <label class="field">Prompt <textarea bind:value={form.prompt} rows="4" placeholder="What the agent does on each run"></textarea></label>
  <AgentPickers agent={form.agent} model={form.model} effort={form.effort} onchange={(v) => Object.assign(form, v)} />
  <label class="row"><input type="checkbox" bind:checked={form.enabled} /> Enabled</label>
  {#snippet footer()}
    {#if editing}<button class="btn danger" type="button" onclick={remove}>Delete</button>{/if}
    <button class="btn primary grow" type="button" disabled={busy || !valid} onclick={save}>{editing ? 'Save' : 'Create job'}</button>
  {/snippet}
</Sheet>

<style>
  .job.off {
    opacity: 0.7;
  }
  .name {
    display: grid;
    gap: 2px;
    text-align: left;
    background: none;
    border: 0;
    padding: 0;
    cursor: pointer;
  }
  .prompt {
    color: var(--ink-2);
    font-size: 0.9rem;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .presets {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  .switch {
    position: relative;
    width: 48px;
    height: 28px;
    flex: none;
  }
  .switch input {
    position: absolute;
    inset: 0;
    opacity: 0;
    margin: 0;
    cursor: pointer;
  }
  .switch span {
    position: absolute;
    inset: 0;
    border-radius: 14px;
    background: var(--line-strong);
    pointer-events: none;
    transition: background 0.15s;
  }
  .switch span::after {
    content: '';
    position: absolute;
    top: 3px;
    left: 3px;
    width: 22px;
    height: 22px;
    border-radius: 50%;
    background: #fff;
    transition: transform 0.15s;
  }
  .switch input:checked + span {
    background: var(--ok);
  }
  .switch input:checked + span::after {
    transform: translateX(20px);
  }
  .switch input:focus-visible + span {
    outline: 2px solid var(--focus);
    outline-offset: 2px;
  }
</style>
