<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';

  let {
    open = $bindable(false),
    title,
    children,
    footer,
  }: { open?: boolean; title: string; children: Snippet; footer?: Snippet } = $props();

  let dialog = $state<HTMLDialogElement>();

  $effect(() => {
    if (!dialog) return;
    if (open && !dialog.open) dialog.showModal();
    else if (!open && dialog.open) dialog.close();
  });

  function onclick(e: MouseEvent) {
    if (e.target === dialog) open = false;
  }
</script>

<dialog bind:this={dialog} onclose={() => (open = false)} {onclick} aria-label={title}>
  {#if open}
    <div class="sheet">
      <header>
        <h2>{title}</h2>
        <button class="icon-btn" type="button" aria-label="Close" onclick={() => (open = false)}><Icon name="close" /></button>
      </header>
      <div class="body">{@render children()}</div>
      {#if footer}<footer>{@render footer()}</footer>{/if}
    </div>
  {/if}
</dialog>

<style>
  dialog {
    padding: 0;
    border: 0;
    background: transparent;
    max-width: 100vw;
    max-height: 100dvh;
    width: 100%;
    margin: auto auto 0;
    color: inherit;
  }
  dialog::backdrop {
    background: rgb(8 12 20 / 45%);
  }
  .sheet {
    background: var(--surface);
    border-radius: var(--r-l) var(--r-l) 0 0;
    box-shadow: var(--shadow-sheet);
    max-height: 92dvh;
    display: flex;
    flex-direction: column;
    animation: rise 0.18s ease-out;
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 8px 8px 0 16px;
  }
  .body {
    padding: 8px 16px 16px;
    overflow-y: auto;
    display: grid;
    gap: 16px;
  }
  footer {
    padding: 12px 16px calc(12px + var(--safe-b));
    border-top: 1px solid var(--line);
    display: flex;
    gap: 8px;
  }
  @keyframes rise {
    from {
      transform: translateY(24px);
      opacity: 0.6;
    }
  }
  @media (min-width: 720px) {
    dialog {
      margin: auto;
      max-width: 560px;
    }
    .sheet {
      border-radius: var(--r-l);
      max-height: 86dvh;
    }
  }
</style>
