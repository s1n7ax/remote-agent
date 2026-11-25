<script lang="ts" generics="T extends string">
  let {
    options,
    value,
    onchange,
    label,
    disabled = false,
  }: {
    options: { value: T; label: string }[];
    value: T | undefined;
    onchange: (v: T) => void;
    label: string;
    disabled?: boolean;
  } = $props();
</script>

<div class="chips" role="radiogroup" aria-label={label}>
  {#each options as o (o.value)}
    <button class={{ on: o.value === value }}
      type="button"
      role="radio"
      aria-checked={o.value === value}
      {disabled}
      onclick={() => o.value !== value && onchange(o.value)}>{o.label}</button>
  {/each}
</div>

<style>
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    padding: 1px;
  }
  .chips::-webkit-scrollbar {
    display: none;
  }
  button {
    flex: none;
    min-height: 36px;
    padding: 0 12px;
    border-radius: 999px;
    border: 1px solid var(--line-strong);
    background: var(--raised);
    color: var(--ink-2);
    font-size: 0.88rem;
    font-weight: 500;
    cursor: pointer;
  }
  button.on {
    background: var(--ink);
    border-color: var(--ink);
    color: var(--bg);
  }
  button:disabled {
    opacity: 0.55;
  }
</style>
