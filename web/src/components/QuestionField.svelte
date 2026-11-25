<script lang="ts">
  import { artifactUrl } from '../lib/api';
  import { isAnswered, otherAllowed } from '../lib/questions';
  import type { Answer, Question } from '../lib/types';
  import Gallery from './Gallery.svelte';
  import Markdown from './Markdown.svelte';

  let {
    q,
    answer,
    onanswer,
    status,
    readonly = false,
  }: {
    q: Question;
    answer: Answer | undefined;
    onanswer: (a: Answer) => void;
    status?: 'saving' | 'saved' | 'error';
    readonly?: boolean;
  } = $props();

  const picked = $derived(answer?.optionIds ?? []);
  const done = $derived(isAnswered(q, answer));
  // svelte-ignore state_referenced_locally
  let text = $state(answer?.text ?? '');
  // svelte-ignore state_referenced_locally
  let otherOpen = $state(!!answer?.text);
  let timer: ReturnType<typeof setTimeout> | undefined;

  function pickSingle(id: string) {
    if (readonly) return;
    otherOpen = false;
    text = '';
    onanswer({ optionIds: [id] });
  }

  function toggleMulti(id: string) {
    if (readonly) return;
    const next = picked.includes(id) ? picked.filter((x) => x !== id) : [...picked, id];
    onanswer({ optionIds: next, text: text.trim() || undefined });
  }

  function openOther() {
    if (readonly) return;
    otherOpen = true;
    if (q.kind === 'single' || q.kind === 'confirm') {
      if (picked.length) onanswer({ optionIds: [], text: text.trim() || undefined });
    }
  }

  function commitText() {
    clearTimeout(timer);
    const t = text.trim();
    if (t === (answer?.text ?? '')) return;
    if (q.kind === 'multi') onanswer({ optionIds: picked, text: t || undefined });
    else onanswer({ optionIds: [], text: t || undefined });
  }

  function oninput() {
    clearTimeout(timer);
    timer = setTimeout(commitText, 900);
  }

  const name = $derived(`q-${q.id}`);
</script>

<fieldset class={['q', { done }]} data-q={q.id} disabled={readonly}>
  <legend>
    <span class="title">{q.title}</span>
    <span class="state" aria-live="polite">
      {#if status === 'saving'}<span class="muted">Saving…</span>
      {:else if status === 'error'}<span class="bad">Not saved</span>
      {:else if done}<span class="ok">✓ Answered</span>{/if}
    </span>
  </legend>
  {#if q.context}<div class="context"><Markdown src={q.context} /></div>{/if}

  {#if q.kind === 'single' || q.kind === 'multi'}
    <div class="options">
      {#each q.options ?? [] as o (o.id)}
        {@const on = picked.includes(o.id)}
        <label class={['opt', { on }]}>
          <input
            type={q.kind === 'single' ? 'radio' : 'checkbox'}
            {name}
            checked={on}
            onchange={() => (q.kind === 'single' ? pickSingle(o.id) : toggleMulti(o.id))} />
          <span class="body">
            <span class="label">
              {o.label}
              {#if o.recommended}<span class="badge primary">Recommended</span>{/if}
            </span>
            {#if o.description}<span class="desc">{o.description}</span>{/if}
          </span>
        </label>
        {#if o.preview && (o.preview.image || o.preview.url || o.preview.markdown)}
          <div class="preview">
            {#if o.preview.image}<Gallery ids={[o.preview.image]} label="{o.label} screenshot" />{/if}
            <div class="pv-side">
              {#if o.preview.markdown}<div class="small"><Markdown src={o.preview.markdown} /></div>{/if}
              {#if o.preview.url}
                <a class="btn small" href={o.preview.url} target="_blank" rel="noopener noreferrer">Open live preview</a>
              {/if}
              {#if o.preview.image}<a class="small" href={artifactUrl(o.preview.image)} target="_blank" rel="noopener noreferrer">Full-size screenshot</a>{/if}
            </div>
          </div>
        {/if}
      {/each}
      {#if otherAllowed(q)}
        {#if q.kind === 'single'}
          <label class={['opt', { on: otherOpen && !picked.length }]}>
            <input type="radio" {name} checked={otherOpen && !picked.length} onchange={openOther} />
            <span class="body"><span class="label">Other…</span></span>
          </label>
        {:else if !otherOpen}
          <button type="button" class="btn small ghost other" onclick={openOther}>+ Add your own answer</button>
        {/if}
        {#if otherOpen && (q.kind === 'multi' || !picked.length)}
          <textarea bind:value={text} {oninput} onblur={commitText} rows="2" placeholder="Your answer" aria-label="Other answer for {q.title}"></textarea>
        {/if}
      {/if}
    </div>
  {:else if q.kind === 'text'}
    <textarea bind:value={text} {oninput} onblur={commitText} rows="3" placeholder="Your answer" aria-label={q.title}></textarea>
  {:else if q.kind === 'confirm'}
    <div class="confirm">
      <button class={['btn', { yes: picked.includes('yes') }]} type="button" onclick={() => pickSingle('yes')}>Yes, that’s right</button>
      <button class={['btn', { no: otherOpen && !picked.includes('yes') }]} type="button" onclick={openOther}>Needs a correction</button>
    </div>
    {#if otherOpen && !picked.includes('yes')}
      <textarea bind:value={text} {oninput} onblur={commitText} rows="3" placeholder="What should change?" aria-label="Correction for {q.title}"></textarea>
    {/if}
  {/if}
</fieldset>

<style>
  .q {
    border: 1px solid var(--line);
    border-radius: var(--r-l);
    padding: 14px;
    margin: 0;
    display: grid;
    gap: 10px;
    background: var(--surface);
    min-width: 0;
  }
  .q.done {
    border-color: color-mix(in srgb, var(--ok) 35%, var(--line));
  }
  legend {
    display: flex;
    align-items: baseline;
    gap: 8px;
    width: 100%;
    padding: 0;
    float: left; /* keeps the legend inside the fieldset's grid flow */
  }
  legend + * {
    clear: both;
  }
  .title {
    flex: 1;
    font-weight: 600;
    font-size: 0.98rem;
    line-height: 1.3;
  }
  .state {
    flex: none;
    font-size: 0.78rem;
    font-weight: 500;
  }
  .ok {
    color: var(--ok);
  }
  .bad {
    color: var(--bad);
  }
  .context {
    color: var(--ink-2);
    font-size: 0.9rem;
  }
  .options {
    display: grid;
    gap: 8px;
  }
  .opt {
    display: flex;
    gap: 12px;
    align-items: flex-start;
    padding: 12px;
    border: 1px solid var(--line-strong);
    border-radius: var(--r-m);
    background: var(--raised);
    cursor: pointer;
  }
  .opt.on {
    border-color: var(--primary);
    background: color-mix(in srgb, var(--primary-soft) 55%, var(--raised));
    box-shadow: inset 0 0 0 1px var(--primary);
  }
  .opt input {
    width: 20px;
    height: 20px;
    margin: 1px 0 0;
    flex: none;
    accent-color: var(--primary);
  }
  .body {
    display: grid;
    gap: 2px;
    min-width: 0;
  }
  .label {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    align-items: center;
    font-weight: 500;
  }
  .desc {
    font-size: 0.86rem;
    color: var(--ink-2);
  }
  .preview {
    display: grid;
    grid-template-columns: 120px minmax(0, 1fr);
    gap: 12px;
    margin: -2px 0 4px 32px;
    align-items: start;
  }
  .pv-side {
    display: grid;
    gap: 8px;
    justify-items: start;
  }
  .other {
    justify-self: start;
    color: var(--primary);
    padding: 0 4px;
  }
  .confirm {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 8px;
  }
  .confirm .btn {
    white-space: normal;
    line-height: 1.2;
    padding: 8px;
  }
  .confirm .yes {
    background: var(--ok-soft);
    border-color: var(--ok);
    color: var(--ok);
  }
  .confirm .no {
    background: var(--attention-soft);
    border-color: var(--attention);
    color: var(--attention-ink);
  }
  fieldset:disabled .opt {
    cursor: default;
  }
</style>
