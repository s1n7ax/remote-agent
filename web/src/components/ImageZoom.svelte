<script lang="ts">
  import Icon from './Icon.svelte';

  /** A full-screen view of one image; src null keeps it closed. */
  let { src, alt, onclose }: { src: string | null; alt: string; onclose: () => void } = $props();
  let dialog = $state<HTMLDialogElement>();

  $effect(() => {
    if (!dialog) return;
    if (src && !dialog.open) dialog.showModal();
    else if (!src && dialog.open) dialog.close();
  });
</script>

<dialog bind:this={dialog} {onclose} onclick={onclose}>
  {#if src}
    <img {src} {alt} />
    <button class="icon-btn close" type="button" aria-label="Close"><Icon name="close" /></button>
  {/if}
</dialog>

<style>
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
