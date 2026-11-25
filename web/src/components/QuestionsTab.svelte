<script lang="ts">
  import { visibility } from '../lib/questions';
  import type { Round, Ticket } from '../lib/types';
  import QuestionField from './QuestionField.svelte';
  import RoundForm from './RoundForm.svelte';

  let { rounds, onsaved }: { rounds: Round[]; onsaved: (r: Round, t: Ticket, completed: boolean) => void } = $props();

  // Rounds completed while this tab is open keep their form, showing the success state.
  let justCompleted = $state<number[]>([]);
  const isOpen = (r: Round) => !r.completedAt || justCompleted.includes(r.n);
  const open = $derived(rounds.filter(isOpen));
  const past = $derived([...rounds.filter((r) => !isOpen(r))].reverse());

  function saved(r: Round, t: Ticket, completed: boolean) {
    if (completed && !justCompleted.includes(r.n)) justCompleted.push(r.n);
    onsaved(r, t, completed);
  }
</script>

<div class="qtab">
  {#each open as r (r.n)}
    <RoundForm round={r} onsaved={saved} />
  {/each}

  {#if !rounds.length}
    <div class="empty">
      <p>No questions yet.</p>
      <p class="small">The agent posts round 1 after it explores the repo.</p>
    </div>
  {/if}

  {#each past as r (r.n)}
    {@const vis = visibility(r.tree, r.answers)}
    <details class="past">
      <summary>
        <span class="grow">Round {r.n} · {r.tree.title}</span>
        <span class="badge ok">Complete</span>
      </summary>
      <div class="past-body">
        {#each r.tree.questions.filter((q) => vis.get(q.id)) as q (q.id)}
          <QuestionField {q} answer={r.answers[q.id]} readonly onanswer={() => {}} />
        {/each}
      </div>
    </details>
  {/each}
</div>

<style>
  .qtab {
    display: grid;
    gap: 16px;
  }
  .past {
    border: 1px solid var(--line);
    border-radius: var(--r-l);
    background: var(--surface);
  }
  summary {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 14px;
    cursor: pointer;
    font-weight: 500;
  }
  .past-body {
    display: grid;
    gap: 10px;
    padding: 0 10px 10px;
  }
</style>
