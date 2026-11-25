<script lang="ts">
  import { renderMarkdown } from '../lib/markdown';

  let { src }: { src: string | undefined } = $props();
  const html = $derived(renderMarkdown(src));
</script>

<!-- eslint-disable-next-line svelte/no-at-html-tags -- sanitized by DOMPurify -->
<div class="md">{@html html}</div>
