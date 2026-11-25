<script lang="ts">
  import type { CIState } from '../lib/types';

  let { pr, ci }: { pr?: number; ci: CIState | undefined } = $props();
  const cls = $derived(ci === 'success' ? 'ok' : ci === 'failure' ? 'bad' : ci === 'pending' ? 'pending' : '');
  const word = $derived(ci === 'success' ? 'CI passed' : ci === 'failure' ? 'CI failed' : ci === 'pending' ? 'CI running' : 'no CI');
</script>

<span class="badge {cls}" title={word}>
  {#if pr}#{pr}{/if}
  <span class="sym" aria-hidden="true">{ci === 'success' ? '✓' : ci === 'failure' ? '✕' : ci === 'pending' ? '●' : '–'}</span>
  <span class="sr">{word}</span>
</span>

<style>
  .sym {
    font-size: 0.72rem;
  }
  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }
</style>
