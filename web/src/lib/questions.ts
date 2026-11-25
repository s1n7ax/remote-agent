// Question-tree visibility and completeness, mirroring internal/questions.
//
// Semantics:
//   - A question without showIf is visible.
//   - {question, anyOf}: true when `question` is visible and its answer's
//     optionIds intersect anyOf.
//   - {question, answered: true}: true when `question` is visible and answered.
//   - {all: [...]}: every condition holds (empty → true).
//   - {any: [...]}: at least one holds (empty → false).
//   - {not: c}: c does not hold.
//   - A condition referencing an unknown question, a hidden question, or
//     (through a cycle) itself is false.
//   - Answers to hidden questions are ignored.
//   - A round is complete when every visible question is answered; a confirm
//     counts as answered with optionIds ["yes"] or a non-empty correction text.

import type { Answer, Condition, Question, QuestionTree } from './types';

export type Answers = Record<string, Answer | undefined>;

/** Reports whether an answer carries anything. */
export function isEmpty(a: Answer | undefined): boolean {
  return !a || ((a.optionIds?.length ?? 0) === 0 && !(a.text ?? '').trim());
}

/** Reports whether `a` answers question `q`. */
export function isAnswered(q: Question, a: Answer | undefined): boolean {
  if (isEmpty(a)) return false;
  if (q.kind === 'confirm') {
    return (a!.optionIds ?? []).includes('yes') || !!(a!.text ?? '').trim();
  }
  return true;
}

/** Reports whether question `q` accepts a free-text answer. */
export function otherAllowed(q: Question): boolean {
  return q.allowOther ?? true;
}

/** Computes the visibility of every question in the tree. */
export function visibility(tree: QuestionTree, answers: Answers): Map<string, boolean> {
  const byId = new Map(tree.questions.map((q) => [q.id, q]));
  const memo = new Map<string, boolean>();
  const visiting = new Set<string>();

  function visible(id: string): boolean {
    const known = memo.get(id);
    if (known !== undefined) return known;
    const q = byId.get(id);
    if (!q) return false;
    if (visiting.has(id)) return false; // cycle
    visiting.add(id);
    const v = q.showIf ? holds(q.showIf) : true;
    visiting.delete(id);
    memo.set(id, v);
    return v;
  }

  function holds(c: Condition): boolean {
    if (c.all) return c.all.every(holds);
    if (c.any) return c.any.some(holds);
    if (c.not) return !holds(c.not);
    if (!c.question) return false;
    const q = byId.get(c.question);
    if (!q || !visible(c.question)) return false;
    const a = answers[c.question];
    if (c.answered) return isAnswered(q, a);
    const picked = a?.optionIds ?? [];
    return (c.anyOf ?? []).some((id) => picked.includes(id));
  }

  for (const q of tree.questions) visible(q.id);
  return memo;
}

export interface Progress {
  answered: number;
  total: number;
  complete: boolean;
  visible: Question[];
}

/** Counts answered visible questions and reports whether the round is complete. */
export function progress(tree: QuestionTree, answers: Answers): Progress {
  const vis = visibility(tree, answers);
  const visibleQs = tree.questions.filter((q) => vis.get(q.id));
  const answered = visibleQs.filter((q) => isAnswered(q, answers[q.id])).length;
  return { answered, total: visibleQs.length, complete: answered === visibleQs.length, visible: visibleQs };
}
