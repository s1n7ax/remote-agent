<script lang="ts">
  import { untrack } from 'svelte';
  import { api } from '../lib/api';
  import { app } from '../lib/app.svelte';
  import { errorText } from '../lib/format';
  import { blindWarning, filesOf, pastedFiles, prepareImage, refusal, seesImages } from '../lib/images';
  import { router } from '../lib/router.svelte';
  import type { AgentKind, Project } from '../lib/types';
  import AgentPickers from './AgentPickers.svelte';
  import Icon from './Icon.svelte';
  import Sheet from './Sheet.svelte';

  let { open = $bindable(false), project, oncreated }: { open?: boolean; project: Project; oncreated: () => void } = $props();

  let idea = $state('');
  // Project defaults, overridable by the pickers.
  let agent = $derived<AgentKind>(project.agent);
  let model = $derived(project.model);
  let effort = $derived(project.effort);
  let prototypes = $state(false);
  let busy = $state(false);

  // Images upload as soon as they are added; the ticket is created with the
  // ids of the ones that made it. A failed one stays with a Retry.
  type Queued = { key: number; file: File; url: string; id?: string; error?: string };
  let queued = $state<Queued[]>([]);
  let refused = $state<string | null>(null);
  let input = $state<HTMLInputElement>();
  let nextKey = 0;
  const uploading = $derived(queued.some((q) => !q.id && !q.error));
  const failed = $derived(queued.some((q) => q.error));

  async function upload(q: Queued) {
    q.error = undefined;
    try {
      q.id = (await api.uploadImage(project.id, q.file)).id;
    } catch (e) {
      q.error = errorText(e);
    }
  }

  async function addFiles(files: File[]) {
    refused = null;
    for (const f of files) {
      let file: File;
      try {
        file = await prepareImage(f);
      } catch (e) {
        refused = `${f.name}: ${errorText(e)}`;
        continue;
      }
      const why = refusal(file, queued.length);
      if (why) {
        refused = `${f.name}: ${why}`;
        continue;
      }
      queued.push({ key: nextKey++, file, url: URL.createObjectURL(file) });
      upload(queued[queued.length - 1]);
    }
    if (input) input.value = '';
  }

  // Uploads belong to the project they were made in; the sheet outlives a
  // project switch, so drop them then.
  let queuedFor: number | null = null;
  $effect(() => {
    const id = project.id;
    if (queuedFor !== null && id !== queuedFor) {
      for (const q of untrack(() => queued)) URL.revokeObjectURL(q.url);
      queued = [];
      refused = null;
    }
    queuedFor = id;
  });

  function unqueue(key: number) {
    const q = queued.find((x) => x.key === key);
    if (q) URL.revokeObjectURL(q.url);
    queued = queued.filter((x) => x.key !== key);
  }

  function onpaste(e: ClipboardEvent) {
    const files = pastedFiles(e.clipboardData);
    if (!files.length) return;
    e.preventDefault();
    addFiles(files);
  }

  async function create(start: boolean) {
    if (!idea.trim() || uploading || failed) return;
    const n = queued.length;
    if (start && n && !seesImages(app.state, agent, model) && !confirm(`${blindWarning(n)}\n\nStart anyway?`)) return;
    busy = true;
    const images = queued.map((q) => q.id!);
    const t = await app.attempt(
      () => api.createTicket(project.id, { idea: idea.trim(), agent, model, effort, prototypes, start, images }),
      start ? 'Ticket created and started' : 'Ticket created in backlog',
    );
    busy = false;
    if (!t) return;
    idea = '';
    for (const q of queued) URL.revokeObjectURL(q.url);
    queued = [];
    refused = null;
    prototypes = false;
    ({ agent, model, effort } = project);
    open = false;
    oncreated();
    if (start) router.go({ name: 'ticket', projectId: project.id, ticketId: t.id, tab: 'log' });
  }
</script>

<Sheet bind:open title="New ticket">
  <div
    class="drop"
    role="group"
    aria-label="New ticket"
    ondragover={(e) => e.preventDefault()}
    ondrop={(e) => {
      e.preventDefault();
      addFiles(filesOf(e.dataTransfer));
    }}>
    <label class="field">
      Rough idea
      <textarea bind:value={idea} rows="5" {onpaste} placeholder="What should change, and why? The agent will ask what it needs."></textarea>
    </label>
    <div class="images">
      {#if queued.length}
        <ul class="queue" aria-label="Images">
          {#each queued as q, i (q.key)}
            <li class={{ failed: q.error }}>
              <img src={q.url} alt="Image {i + 1}" />
              <span class="num" aria-hidden="true">{i + 1}</span>
              <button type="button" class="remove" aria-label="Remove image {i + 1}" onclick={() => unqueue(q.key)}><Icon name="close" size={14} /></button>
              {#if q.error}
                <button type="button" class="state retry" onclick={() => upload(q)} title={q.error}>Retry</button>
              {:else if !q.id}
                <span class="state">Uploading…</span>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
      <button class="btn small" type="button" onclick={() => input?.click()}><Icon name="plus" size={16} /> Add images</button>
      <input bind:this={input} type="file" accept="image/*" multiple hidden onchange={(e) => addFiles(Array.from(e.currentTarget.files ?? []))} />
      {#each queued.filter((q) => q.error) as q (q.key)}
        <p class="error" role="alert">Image {queued.indexOf(q) + 1} did not upload: {q.error}</p>
      {/each}
      {#if refused}<p class="error" role="alert">{refused}</p>{/if}
    </div>
  </div>
  <AgentPickers {agent} {model} {effort} images={queued.length} onchange={(v) => ({ agent, model, effort } = v)} />
  <label class="toggle">
    <input type="checkbox" bind:checked={prototypes} />
    <span>
      <strong>Prototypes</strong>
      <span class="muted small">Build 2–3 options with screenshots and a live preview to pick from.</span>
    </span>
  </label>
  {#snippet footer()}
    <button class="btn grow" type="button" disabled={busy || uploading || failed || !idea.trim()} onclick={() => create(false)}>Create</button>
    <button class="btn primary grow" type="button" disabled={busy || uploading || failed || !idea.trim()} onclick={() => create(true)}>Create &amp; start</button>
  {/snippet}
</Sheet>

<style>
  .drop,
  .images {
    display: grid;
    gap: 10px;
  }
  .images > .btn {
    justify-self: start;
  }
  .queue {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(84px, 1fr));
    gap: 8px;
  }
  .queue li {
    position: relative;
    aspect-ratio: 1;
    border: 1px solid var(--line);
    border-radius: var(--r-m);
    overflow: hidden;
    background: var(--sunken);
  }
  .queue li.failed {
    border-color: var(--bad);
  }
  .queue img {
    width: 100%;
    height: 100%;
    object-fit: cover;
    display: block;
  }
  .num,
  .remove,
  .state {
    position: absolute;
    background: rgb(0 0 0 / 62%);
    color: #fff;
    border: 0;
    font-size: 0.76rem;
    font-weight: 600;
  }
  .num {
    top: 4px;
    left: 4px;
    min-width: 20px;
    height: 20px;
    border-radius: 10px;
    display: grid;
    place-items: center;
    padding: 0 5px;
  }
  .remove {
    top: 2px;
    right: 2px;
    width: 28px;
    height: 28px;
    border-radius: 14px;
    display: grid;
    place-items: center;
    cursor: pointer;
  }
  .state {
    left: 0;
    right: 0;
    bottom: 0;
    padding: 4px;
    text-align: center;
  }
  .retry {
    background: var(--bad);
    cursor: pointer;
  }
  .error {
    color: var(--bad);
    font-size: 0.88rem;
    margin: 0;
  }
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
