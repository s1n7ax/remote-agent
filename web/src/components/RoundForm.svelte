<script lang="ts">
  import { api } from '../lib/api';
  import { app } from '../lib/app.svelte';
  import { errorText } from '../lib/format';
  import { progress } from '../lib/questions';
  import type { Answer, Round, Ticket } from '../lib/types';
  import Markdown from './Markdown.svelte';
  import QuestionField from './QuestionField.svelte';

  let { round, onsaved }: { round: Round; onsaved: (r: Round, t: Ticket, completed: boolean) => void } = $props();

  // svelte-ignore state_referenced_locally
  let answers = $state<Record<string, Answer>>({ ...round.answers });
  // Text typed but not yet saved; it counts towards Submit right away.
  let drafts = $state<Record<string, Answer>>({});
  let status = $state<Record<string, 'saving' | 'saved' | 'error'>>({});
  // svelte-ignore state_referenced_locally
  let completed = $state(!!round.completedAt);
  let submitting = $state(false);
  const prog = $derived(progress(round.tree, { ...answers, ...drafts }));
  const pending: Record<string, Promise<unknown>> = {};

  async function save(qid: string, a: Answer) {
    if (completed) return;
    answers[qid] = a;
    delete drafts[qid];
    status[qid] = 'saving';
    // Serialize saves per question so the last edit wins.
    const prev = pending[qid] ?? Promise.resolve();
    const p = prev.then(async () => {
      try {
        const res = await api.answer(round.ticketId, round.n, qid, a);
        status[qid] = 'saved';
        onsaved(res.round, res.ticket, false);
      } catch (e) {
        status[qid] = 'error';
        app.notify(`Answer not saved: ${errorText(e)}`, 'error');
      }
    });
    pending[qid] = p;
    await p;
  }

  async function submit() {
    if (completed || submitting || !prog.complete) return;
    submitting = true;
    try {
      // Save the text typed just before the tap and retry failed saves, then
      // wait for every save; a save that still fails has shown its toast.
      for (const q of prog.visible) {
        const a = drafts[q.id] ?? (status[q.id] === 'error' ? answers[q.id] : undefined);
        if (a) void save(q.id, a);
      }
      await Promise.all(Object.values(pending));
      if (prog.visible.some((q) => status[q.id] === 'error')) return;
      const res = await api.submitRound(round.ticketId, round.n);
      completed = true;
      onsaved(res.round, res.ticket, true);
    } catch (e) {
      app.notify(`Round not submitted: ${errorText(e)}`, 'error');
    } finally {
      submitting = false;
    }
  }
</script>

<section class="round">
  <header class="head">
    <div class="grow">
      <h2>Round {round.n} · {round.tree.title}</h2>
      <p class="muted small">Answers save as you go. Come back any time.</p>
    </div>
    <div class="meter" role="status" aria-label="{prog.answered} of {prog.total} answered">
      <span class="num">{prog.answered} of {prog.total}</span>
      <span class="muted small">answered</span>
      <span class="bar"><span style:width="{prog.total ? (prog.answered / prog.total) * 100 : 0}%"></span></span>
    </div>
  </header>

  {#if round.tree.intro}<div class="intro"><Markdown src={round.tree.intro} /></div>{/if}

  {#if completed}
    <div class="success" role="status">
      <strong>Round {round.n} is complete.</strong>
      <span>The ticket moved to Ready; the next agent picks it up now.</span>
    </div>
  {/if}

  {#each prog.visible as q (q.id)}
    <QuestionField
      {q}
      answer={answers[q.id]}
      status={status[q.id]}
      readonly={completed || submitting}
      onanswer={(a) => save(q.id, a)}
      ondraft={(a) => (drafts[q.id] = a)} />
  {/each}

  {#if !completed}
    <div class="submit">
      <button class="btn primary" type="button" disabled={!prog.complete || submitting} onclick={submit}>
        {submitting ? 'Submitting…' : 'Submit'}
      </button>
      <p class="muted small">
        {prog.complete ? 'Submitting is final: the answers lock and the next run starts.' : 'Answer every question to submit.'}
      </p>
    </div>
  {/if}
</section>

<style>
  .round {
    display: grid;
    gap: 12px;
  }
  .head {
    display: flex;
    gap: 12px;
    align-items: flex-start;
    position: sticky;
    top: calc(var(--topbar-h) + 44px);
    z-index: 2;
    background: var(--bg);
    padding: 10px 0;
  }
  .meter {
    display: grid;
    justify-items: end;
    gap: 2px;
    flex: none;
  }
  .num {
    font-weight: 600;
    font-size: 1.05rem;
  }
  .bar {
    width: 88px;
    height: 6px;
    border-radius: 3px;
    background: var(--sunken);
    overflow: hidden;
  }
  .bar span {
    display: block;
    height: 100%;
    background: var(--attention);
    transition: width 0.25s ease;
  }
  .intro {
    color: var(--ink-2);
  }
  .submit {
    display: grid;
    gap: 6px;
    justify-items: center;
    text-align: center;
  }
  .submit .btn {
    width: 100%;
  }
  .submit p {
    margin: 0;
  }
  .success {
    display: grid;
    gap: 2px;
    padding: 14px;
    border-radius: var(--r-l);
    background: var(--ok-soft);
    color: var(--ok);
    border: 1px solid color-mix(in srgb, var(--ok) 40%, transparent);
  }
</style>
