<script lang="ts">
  import { artifactUrl } from '../lib/api';
  import Icon from './Icon.svelte';

  let {
    ids,
    label = 'Screenshot',
    numbers,
    onremove,
    aspect = '9 / 16',
  }: {
    ids: string[];
    label?: string;
    /** Shown numbers, one per id (default 1..n), badged on each thumbnail when given. */
    numbers?: number[];
    /** Adds a remove button per thumbnail. */
    onremove?: (index: number) => void;
    aspect?: string;
  } = $props();
  const num = (i: number) => numbers?.[i] ?? i + 1;
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
    <div class="item" style:aspect-ratio={aspect}>
      <button type="button" class="thumb" onclick={() => (zoomed = id)} aria-label="Zoom {label.toLowerCase()} {num(i)}">
        <img src={artifactUrl(id)} alt="{label} {num(i)}" loading="lazy" />
      </button>
      {#if numbers}<span class="num" aria-hidden="true">{num(i)}</span>{/if}
      {#if onremove}
        <button type="button" class="remove" aria-label="Remove {label.toLowerCase()} {num(i)}" onclick={() => onremove(i)}><Icon name="close" size={14} /></button>
      {/if}
    </div>
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
  .item {
    position: relative;
  }
  .thumb {
    padding: 0;
    border: 1px solid var(--line);
    border-radius: var(--r-m);
    overflow: hidden;
    background: var(--sunken);
    cursor: zoom-in;
    width: 100%;
    height: 100%;
  }
  .num {
    position: absolute;
    top: 6px;
    left: 6px;
    min-width: 22px;
    height: 22px;
    padding: 0 6px;
    border-radius: 11px;
    background: rgb(0 0 0 / 62%);
    color: #fff;
    font-size: 0.78rem;
    font-weight: 600;
    display: grid;
    place-items: center;
    pointer-events: none;
  }
  .remove {
    position: absolute;
    top: 4px;
    right: 4px;
    width: 28px;
    height: 28px;
    border: 0;
    border-radius: 14px;
    background: rgb(0 0 0 / 62%);
    color: #fff;
    display: grid;
    place-items: center;
    cursor: pointer;
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
