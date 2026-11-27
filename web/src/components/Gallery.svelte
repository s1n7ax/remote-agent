<script lang="ts">
  import { artifactUrl } from '../lib/api';
  import ImageZoom from './ImageZoom.svelte';

  let { ids, label = 'Screenshot' }: { ids: string[]; label?: string } = $props();
  let zoomed = $state<string | null>(null);
</script>

<div class="gallery">
  {#each ids as id, i (id)}
    <button type="button" class="thumb" onclick={() => (zoomed = id)} aria-label="Zoom {label.toLowerCase()} {i + 1}">
      <img src={artifactUrl(id)} alt="{label} {i + 1}" loading="lazy" />
    </button>
  {/each}
</div>

<ImageZoom src={zoomed && artifactUrl(zoomed)} alt="{label} enlarged" onclose={() => (zoomed = null)} />

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
</style>
