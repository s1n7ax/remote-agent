<script lang="ts">
  import { api } from '../lib/api';
  import { app } from '../lib/app.svelte';
  import { errorText } from '../lib/format';
  import { filesOf, prepareImage, refusal } from '../lib/images';
  import type { TicketImage } from '../lib/types';
  import Gallery from './Gallery.svelte';
  import Icon from './Icon.svelte';

  let { ticketId, images = $bindable() }: { ticketId: number; images: TicketImage[] } = $props();

  let busy = $state(false);
  let error = $state<string | null>(null);
  let input = $state<HTMLInputElement>();

  async function add(files: File[]) {
    if (!files.length || busy) return;
    busy = true;
    error = null;
    for (const f of files) {
      try {
        const file = await prepareImage(f);
        const why = refusal(file, images.length);
        if (why) throw new Error(why);
        images = [...images, await api.addImage(ticketId, file)];
      } catch (e) {
        error = `${f.name}: ${errorText(e)}`;
        break;
      }
    }
    busy = false;
    if (input) input.value = '';
  }

  async function remove(i: number) {
    const im = images[i];
    if (!confirm(`Remove image ${im.n}? Runs that start from now on won't see it.`)) return;
    const ok = await app.attempt(() => api.removeImage(ticketId, im.n), `Image ${im.n} removed`);
    if (ok) images = images.filter((x) => x.n !== im.n);
  }

  function onpaste(e: ClipboardEvent) {
    const files = filesOf(e.clipboardData);
    if (!files.length) return;
    e.preventDefault();
    add(files);
  }
</script>

<svelte:document {onpaste} />

<section
  class="panel images"
  aria-label="Images"
  ondragover={(e) => e.preventDefault()}
  ondrop={(e) => {
    e.preventDefault();
    add(filesOf(e.dataTransfer));
  }}>
  <div class="row head">
    <h2>Images</h2>
    <button class="btn small" type="button" disabled={busy} onclick={() => input?.click()}>
      <Icon name="plus" size={16} />
      {busy ? 'Adding…' : 'Add'}
    </button>
    <input bind:this={input} type="file" accept="image/*" multiple hidden onchange={(e) => add(Array.from(e.currentTarget.files ?? []))} />
  </div>
  {#if images.length}
    <Gallery ids={images.map((im) => im.artifactId)} numbers={images.map((im) => im.n)} label="Image" aspect="1" onremove={remove} />
  {:else}
    <p class="muted small">Screenshots or photos the agent should look at. Refer to them by number in your text.</p>
  {/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</section>

<style>
  .images {
    gap: 10px;
  }
  .head {
    justify-content: space-between;
    align-items: center;
  }
  .head h2 {
    margin: 0;
  }
  .error {
    color: var(--bad);
    font-size: 0.88rem;
    margin: 0;
  }
</style>
