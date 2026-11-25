<script lang="ts">
  import { api } from '../lib/api';
  import { app } from '../lib/app.svelte';
  import type { PreviewInfo, Prototype } from '../lib/types';
  import Gallery from './Gallery.svelte';

  let {
    ticketId,
    prototypes,
    preview,
    onchange,
  }: { ticketId: number; prototypes: Prototype[]; preview: PreviewInfo | undefined; onchange: () => void } = $props();

  let busy = $state<string | null>(null);

  async function start(p: Prototype) {
    busy = p.name;
    const res = await app.attempt(() => api.startPreview(ticketId, p.name), `Preview of ${p.name} is starting`);
    busy = null;
    if (res) {
      if (app.state) app.state.preview = res;
      onchange();
    }
  }

  async function stop() {
    busy = 'stop';
    const res = await app.attempt(() => api.stopPreview(), 'Preview stopped');
    busy = null;
    if (res) {
      if (app.state) app.state.preview = null;
      onchange();
    }
  }
</script>

<section class="protos">
  <div class="row spread">
    <h2>Prototypes</h2>
    {#if preview}<button class="btn small" type="button" disabled={busy !== null} onclick={stop}>Stop preview</button>{/if}
  </div>
  <p class="muted small">One live preview runs at a time. Pick the one that ships in the questions below.</p>
  {#each prototypes as p (p.name)}
    {@const live = preview?.branch === p.branch}
    <article class={['proto', { live }]}>
      <div class="row spread wrap">
        <h3>{p.name}</h3>
        {#if live}
          <a class="btn small primary" href={preview!.url} target="_blank" rel="noopener noreferrer">Open live preview</a>
        {:else if p.preview}
          <button class="btn small" type="button" disabled={busy !== null} onclick={() => start(p)}>
            {busy === p.name ? 'Starting…' : 'Start live preview'}
          </button>
        {/if}
      </div>
      {#if p.description}<p class="small">{p.description}</p>{/if}
      {#if p.artifacts?.length}<Gallery ids={p.artifacts} label="{p.name} screenshot" />{/if}
      <p class="muted small mono">{p.branch}</p>
    </article>
  {/each}
</section>

<style>
  .protos {
    display: grid;
    gap: 10px;
  }
  .proto {
    display: grid;
    gap: 8px;
    padding: 14px;
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--r-l);
  }
  .proto.live {
    border-color: var(--primary);
    box-shadow: inset 0 0 0 1px var(--primary);
  }
</style>
