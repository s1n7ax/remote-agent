<script lang="ts">
  import { artifactUrl } from '../lib/api';
  import Icon from './Icon.svelte';

  let { ids, label = 'Screenshot' }: { ids: string[]; label?: string } = $props();
  let zoomed = $state<string | null>(null);
  let dialog = $state<HTMLDialogElement>();

  $effect(() => {
    if (!dialog) return;
    if (zoomed && !dialog.open) dialog.showModal();
    else if (!zoomed && dialog.open) dialog.close();
  });
</script>

<div class="gallery">
  {#each ids as id, i (id)}
    <button type="button" class="thumb" onclick={() => (zoomed = id)} aria-label="Zoom {label.toLowerCase()} {i + 1}">
      <img src={artifactUrl(id)} alt="{label} {i + 1}" loading="lazy" />
    </button>
  {/each}
</div>

<dialog bind:this={dialog} onclose={() => (zoomed = null)} onclick={() => (zoomed = null)}>
  {#if zoomed}
    <img src={artifactUrl(zoomed)} alt="{label} enlarged" />
    <button class="icon-btn close" type="button" aria-label="Close"><Icon name="close" /></button>
  {/if}
</dialog>

<style>
  .gallery {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(120px, 1fr));
    gap: 8px;
  }
  .thumb {
    padding: 0;
    border: 1px solid var(--line);
    border-radius: var(--r-m);
    overflow: hidden;
    background: var(--sunken);
    cursor: zoom-in;
    aspect-ratio: 9 / 16;
  }
  .thumb img {
    width: 100%;
    height: 100%;
    object-fit: cover;
    object-position: top;
    display: block;
  }
  dialog {
    border: 0;
    padding: 0;
    background: transparent;
    max-width: 100vw;
    max-height: 100dvh;
    width: 100vw;
    height: 100dvh;
    overflow: auto;
  }
  dialog::backdrop {
    background: rgb(5 8 12 / 88%);
  }
  dialog img {
    display: block;
    max-width: 100%;
    margin: 0 auto;
    touch-action: pinch-zoom;
  }
  .close {
    position: fixed;
    top: 8px;
    right: 8px;
    background: rgb(0 0 0 / 50%);
    color: #fff;
  }
</style>
