<script lang="ts">
  import { onMount } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import { api } from '../lib/api';
  import { app } from '../lib/app.svelte';
  import { errorText } from '../lib/format';
  import { href, router } from '../lib/router.svelte';
  import type { BenchmarkResult, SkillSummary } from '../lib/types';

  let { skill, file }: { skill?: string; file?: string } = $props();

  let skills = $state<SkillSummary[] | null>(null);
  let error = $state<string | null>(null);
  let bench = $state<BenchmarkResult[]>([]);
  let poll: ReturnType<typeof setTimeout> | undefined;
  let benching = $state(false);

  let content = $state('');
  let original = $state('');
  let message = $state('');
  let loadingFile = $state(false);
  let saving = $state(false);

  const current = $derived(skills?.find((s) => s.name === skill));
  const path = $derived(file ?? (current?.files.includes('SKILL.md') ? 'SKILL.md' : current?.files[0]));
  const dirty = $derived(content !== original);

  async function load() {
    try {
      skills = (await api.skills()) ?? [];
      error = null;
    } catch (e) {
      error = errorText(e);
    }
  }

  $effect(() => {
    load();
  });

  $effect(() => {
    if (!skill || !path) return;
    const name = skill;
    const p = path;
    loadingFile = true;
    api
      .skillFile(name, p)
      .then((f) => {
        content = original = f.content;
        message = `skills(${name}): update ${p}`;
      })
      .catch((e) => app.notify(errorText(e), 'error'))
      .finally(() => (loadingFile = false));
  });

  async function save() {
    if (!skill || !path) return;
    saving = true;
    const res = await app.attempt(() => api.saveSkillFile(skill!, path!, content, message.trim()));
    saving = false;
    if (res) {
      original = content;
      app.notify(`Committed ${res.commit.slice(0, 7)} — the next run uses it`);
      load();
    }
  }

  const running = $derived(bench.some((r) => r.status === 'running'));

  async function loadBench() {
    clearTimeout(poll);
    try {
      bench = (await api.benchmarkResults()) ?? [];
    } catch {
      // results are optional on this page
    }
    if (bench.some((r) => r.status === 'running')) poll = setTimeout(loadBench, 5000);
  }

  onMount(() => {
    loadBench();
    return () => clearTimeout(poll);
  });

  async function runBench() {
    benching = true;
    const res = await app.attempt(() => api.benchmark());
    benching = false;
    if (res) {
      app.notify(`Benchmark started: ${res.started.join(', ')}`);
      loadBench();
    }
  }

  // One tap removes (a commit in the skills repo); the toast offers Undo,
  // which restores the skill from the commit before the removal.
  async function remove(name: string) {
    const res = await app.attempt(() => api.deleteSkill(name));
    if (!res) return;
    await load();
    if (skill === name) router.go({ name: 'skills' });
    app.notify(`Removed ${name}`, 'ok', {
      label: 'Undo',
      run: async () => {
        if (await app.attempt(() => api.restoreSkill(name, res.commit), `Restored ${name}`)) await load();
      },
    });
  }

  const pct = (n: number) => `${Math.round(n * 100)}%`;
  const signed = (n: number) => `${n > 0 ? '+' : n < 0 ? '−' : '±'}${Math.abs(Math.round(n * 100))}`;
</script>

<div class="page">
  {#if error}
    <div class="empty"><p>Could not load skills: {error}</p></div>
  {:else if !skills}
    <p class="muted">Loading…</p>
  {:else if skill && current}
    <a class="back" href={href({ name: 'skills' })}><Icon name="back" size={18} /> Skills</a>
    <div class="row spread">
      <h1>{current.name}</h1>
      <button class="btn small danger" type="button" onclick={() => current && remove(current.name)}>Remove</button>
    </div>
    <nav class="files" aria-label="Files">
      {#each current.files as f (f)}
        <a class={['mono', { on: f === path }]} href={href({ name: 'skills', skill: current.name, file: f })}>{f}</a>
      {/each}
    </nav>
    <textarea class="editor mono" bind:value={content} spellcheck="false" autocapitalize="off" disabled={loadingFile} aria-label="Contents of {path}"></textarea>
    <div class="commit">
      <label class="field grow">Commit message <input type="text" bind:value={message} /></label>
      <button class="btn primary" type="button" disabled={!dirty || saving || !message.trim()} onclick={save}>{saving ? 'Committing…' : 'Save & commit'}</button>
    </div>
  {:else if skill}
    <div class="empty"><p>No skill named “{skill}”.</p><a class="btn" href={href({ name: 'skills' })}>All skills</a></div>
  {:else}
    <div class="row spread">
      <h1>Skills</h1>
      <button class="btn small" type="button" disabled={benching || running} onclick={runBench}>{running ? 'Benchmark running…' : 'Run benchmark'}</button>
    </div>
    {#if bench.length}
      <section class="panel">
        <h2>With vs. without skill</h2>
        <table>
          <thead><tr><th>Skill</th><th>With</th><th>Without</th><th>Δ</th></tr></thead>
          <tbody>
            {#each bench as r (r.skill)}
              <tr>
                <td>
                  {r.skill}
                  {#if r.status === 'running'}<span class="badge pending">running</span>{:else if r.status === 'failed'}<span class="badge bad" title={r.error}>failed</span>{:else if r.verdict}<span class="muted small">{r.verdict}</span>{/if}
                </td>
                {#if r.status === 'done'}
                  <td>{pct(r.with)}</td>
                  <td>{pct(r.without)}</td>
                  <td class={{ up: r.delta > 0.005, down: r.delta < -0.005 }}>{signed(r.delta)}</td>
                {:else}
                  <td class="muted" colspan="3">{r.status === 'failed' ? (r.error ?? 'failed') : '…'}</td>
                {/if}
              </tr>
            {/each}
          </tbody>
        </table>
        <p class="muted small">Pass rate of each skill’s evals with the skill loaded and with it removed. Δ in percentage points.</p>
      </section>
    {/if}
    <ul class="list">
      {#each skills as s (s.name)}
        <li>
          <a href={href({ name: 'skills', skill: s.name })} class="grow">
            <strong>{s.name}</strong>
            <span class="muted small">{s.description}</span>
          </a>
          {#if s.hasEvals}<span class="badge">evals</span>{/if}
          <button class="icon-btn" type="button" aria-label="Remove {s.name}" onclick={() => remove(s.name)}><Icon name="trash" size={18} /></button>
        </li>
      {:else}
        <li class="muted">No skills in the repo.</li>
      {/each}
    </ul>
  {/if}
</div>


<style>
  .back {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--ink-2);
    text-decoration: none;
    font-size: 0.88rem;
  }
  .files {
    display: flex;
    gap: 6px;
    overflow-x: auto;
    scrollbar-width: none;
  }
  .files a {
    flex: none;
    padding: 6px 10px;
    border-radius: var(--r-s);
    border: 1px solid var(--line);
    background: var(--surface);
    color: var(--ink-2);
    text-decoration: none;
  }
  .files a.on {
    border-color: var(--ink);
    color: var(--ink);
  }
  .editor {
    min-height: 52dvh;
    font-size: 14px;
    line-height: 1.5;
    tab-size: 2;
    /* Skills are prose; wrap so nothing runs off a phone screen. */
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .commit {
    display: flex;
    gap: 8px;
    align-items: flex-end;
    flex-wrap: wrap;
  }
  .commit .field {
    min-width: 220px;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.9rem;
  }
  th,
  td {
    text-align: right;
    padding: 8px 4px;
    border-bottom: 1px solid var(--line);
  }
  th:first-child,
  td:first-child {
    text-align: left;
  }
  th {
    font-weight: 500;
    color: var(--muted);
    font-size: 0.8rem;
  }
  td.up {
    color: var(--ok);
    font-weight: 600;
  }
  td.down {
    color: var(--bad);
    font-weight: 600;
  }
  .list {
    list-style: none;
    margin: 0;
    padding: 0;
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--r-l);
  }
  .list li {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 6px 6px 14px;
    border-bottom: 1px solid var(--line);
  }
  .list li:last-child {
    border-bottom: 0;
  }
  .list a {
    display: grid;
    gap: 2px;
    padding: 6px 0;
    color: var(--ink);
    text-decoration: none;
  }
</style>
