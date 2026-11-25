<script lang="ts">
  import { clock } from '../lib/format';
  import type { Proof } from '../lib/types';
  import Gallery from './Gallery.svelte';
  import Markdown from './Markdown.svelte';

  let { proof }: { proof: Proof | null | undefined } = $props();
</script>

{#if !proof}
  <div class="empty"><p>No proof yet.</p><p class="small">The verify stage attaches its report and screenshots here.</p></div>
{:else}
  <div class="proof">
    <div class={['verdict', { pass: proof.verdict === 'PASS' }]}>
      <span class="v">{proof.verdict === 'PASS' ? 'Passed' : proof.verdict === 'FAIL' ? 'Failed' : proof.verdict}</span>
      <span class="small">Verified {clock(proof.createdAt)}</span>
    </div>
    {#if proof.files.length}
      <section class="panel">
        <h3>Screenshots</h3>
        <Gallery ids={proof.files} />
      </section>
    {/if}
    <section class="panel"><Markdown src={proof.report} /></section>
  </div>
{/if}

<style>
  .proof {
    display: grid;
    gap: 12px;
  }
  .verdict {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    padding: 14px 16px;
    border-radius: var(--r-l);
    background: var(--bad-soft);
    color: var(--bad);
  }
  .verdict.pass {
    background: var(--ok-soft);
    color: var(--ok);
  }
  .v {
    font-size: 1.15rem;
    font-weight: 600;
  }
</style>
