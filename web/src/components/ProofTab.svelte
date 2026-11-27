<script lang="ts">
  import { artifactUrl } from '../lib/api';
  import { clock } from '../lib/format';
  import { renderMarkdown } from '../lib/markdown';
  import { proofArtifactId } from '../lib/proof';
  import type { Proof } from '../lib/types';
  import Gallery from './Gallery.svelte';
  import ImageZoom from './ImageZoom.svelte';

  let { proof }: { proof: Proof | null | undefined } = $props();
  const html = $derived(renderMarkdown(proof?.report));
  let body = $state<HTMLDivElement>();
  let zoomed = $state<string | null>(null);

  function unavailable(): HTMLElement {
    const el = document.createElement('span');
    el.className = 'shot-missing';
    el.textContent = 'Screenshot unavailable';
    return el;
  }

  // The report's images become zoomable screenshots; one that points at no
  // stored artifact, or fails to load, becomes a placeholder.
  function inlineScreenshots(root: HTMLElement): void {
    for (const img of Array.from(root.querySelectorAll('img'))) {
      const id = proofArtifactId(img.getAttribute('src'));
      if (!id) {
        img.replaceWith(unavailable());
        continue;
      }
      const shot = document.createElement('button');
      shot.type = 'button';
      shot.className = 'shot';
      shot.setAttribute('aria-label', `Zoom ${img.alt || 'screenshot'}`);
      shot.onclick = () => (zoomed = id);
      img.replaceWith(shot);
      shot.append(img);
      img.onerror = () => shot.replaceWith(unavailable());
      img.src = artifactUrl(id);
    }
  }

  $effect(() => {
    void html;
    if (body) inlineScreenshots(body);
  });
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
    <section class="panel">
      {#key html}
        <!-- eslint-disable-next-line svelte/no-at-html-tags -- sanitized by DOMPurify -->
        <div class="md report" bind:this={body}>{@html html}</div>
      {/key}
    </section>
  </div>
  <ImageZoom src={zoomed && artifactUrl(zoomed)} alt="Screenshot enlarged" onclose={() => (zoomed = null)} />
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
  .report :global(table:has(.shot, .shot-missing)) {
    table-layout: fixed;
    width: 100%;
  }
  .report :global(.shot) {
    display: block;
    padding: 0;
    border: 1px solid var(--line);
    border-radius: var(--r-m);
    overflow: hidden;
    background: var(--sunken);
    cursor: zoom-in;
    width: min(100%, 320px);
  }
  .report :global(.shot img) {
    display: block;
    width: 100%;
    height: auto;
  }
  .report :global(.shot-missing) {
    display: flex;
    align-items: center;
    justify-content: center;
    width: min(100%, 320px);
    min-height: 80px;
    padding: 8px 12px;
    border: 1px dashed var(--line);
    border-radius: var(--r-m);
    background: var(--sunken);
    color: var(--muted);
    font-size: 0.85rem;
    text-align: center;
  }
</style>
