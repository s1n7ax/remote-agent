<script lang="ts">
  import PrPanel from '../components/PrPanel.svelte';
  import { api } from '../lib/api';
  import { errorText } from '../lib/format';
  import type { GitHubView, Project } from '../lib/types';

  let { project }: { project: Project } = $props();
  let view = $state<GitHubView | null>(null);
  let error = $state<string | null>(null);

  async function load() {
    try {
      const v = await api.github(project.id);
      v.pulls ??= [];
      v.issues ??= [];
      view = v;
      error = null;
    } catch (e) {
      error = errorText(e);
    }
  }

  $effect(() => {
    void project.id;
    view = null;
    load();
  });

  const open = $derived((view?.pulls ?? []).filter((p) => p.state === 'open' && !p.merged));
  const ticketFor = (pr: number) => view?.ticketsByPr?.[String(pr)];
</script>

<div class="page">
  <div class="row spread">
    <h1>{project.repo}</h1>
    <button class="btn small" type="button" onclick={load}>Refresh</button>
  </div>
  {#if error}
    <div class="empty"><p>Could not reach GitHub: {error}</p></div>
  {:else if !view}
    <p class="muted">Loading…</p>
  {:else}
    <section class="sec">
      <h2>Pull requests <span class="muted">{open.length}</span></h2>
      {#each open as pr (pr.number)}
        <PrPanel {pr} projectId={project.id} ticketId={ticketFor(pr.number)} onmerged={load} />
      {:else}
        <p class="muted">No open pull requests.</p>
      {/each}
    </section>
    <section class="sec">
      <h2>Open issues <span class="muted">{view.issues?.length ?? 0}</span></h2>
      <ul class="issues">
        {#each view.issues ?? [] as i (i.number)}
          <li>
            <a href={i.url} target="_blank" rel="noopener noreferrer" class="grow">
              <span class="muted">#{i.number}</span> {i.title}
            </a>
            <div class="row wrap">
              {#each i.labels ?? [] as l (l)}<span class="badge">{l}</span>{/each}
              <span class="muted small">by {i.author}</span>
            </div>
          </li>
        {:else}
          <li class="muted">No open issues.</li>
        {/each}
      </ul>
    </section>
  {/if}
</div>

<style>
  h1 {
    overflow-wrap: anywhere;
  }
  .sec {
    display: grid;
    gap: 10px;
  }
  .issues {
    list-style: none;
    margin: 0;
    padding: 0;
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--r-l);
  }
  .issues li {
    display: grid;
    gap: 6px;
    padding: 12px 14px;
    border-bottom: 1px solid var(--line);
  }
  .issues li:last-child {
    border-bottom: 0;
  }
  .issues a.grow {
    color: var(--ink);
    text-decoration: none;
    font-weight: 500;
  }
</style>
