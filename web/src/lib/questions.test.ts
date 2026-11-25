import { describe, expect, it } from 'vitest';
import { isAnswered, progress, visibility } from './questions';
import type { Question, QuestionTree } from './types';

const q = (id: string, extra: Partial<Question> = {}): Question => ({ id, kind: 'single', title: id, options: [{ id: 'a', label: 'A' }, { id: 'b', label: 'B' }], ...extra });

describe('visibility', () => {
  const tree: QuestionTree = {
    title: 't',
    questions: [
      q('root'),
      q('child', { showIf: { question: 'root', anyOf: ['a'] } }),
      q('grandchild', { showIf: { question: 'child', anyOf: ['b'] } }),
      q('answered', { showIf: { question: 'root', answered: true } }),
      q('all', { showIf: { all: [{ question: 'root', anyOf: ['a'] }, { question: 'child', anyOf: ['a'] }] } }),
      q('any', { showIf: { any: [{ question: 'root', anyOf: ['b'] }, { question: 'child', anyOf: ['a'] }] } }),
      q('not', { showIf: { not: { question: 'root', anyOf: ['a'] } } }),
      q('unknown', { showIf: { question: 'nope', anyOf: ['a'] } }),
      q('cycleA', { showIf: { question: 'cycleB', answered: true } }),
      q('cycleB', { showIf: { question: 'cycleA', answered: true } }),
    ],
  };

  it('shows unconditional questions and hides dependents until their parent matches', () => {
    const v = visibility(tree, {});
    expect(v.get('root')).toBe(true);
    expect(v.get('child')).toBe(false);
    expect(v.get('grandchild')).toBe(false);
    expect(v.get('answered')).toBe(false);
    expect(v.get('not')).toBe(true);
  });

  it('evaluates anyOf, answered, all, any, not', () => {
    const v = visibility(tree, { root: { optionIds: ['a'] }, child: { optionIds: ['a'] } });
    expect(v.get('child')).toBe(true);
    expect(v.get('answered')).toBe(true);
    expect(v.get('all')).toBe(true);
    expect(v.get('any')).toBe(true);
    expect(v.get('not')).toBe(false);
  });

  it('hides a question whose condition references a hidden question, even if that answer matches', () => {
    // child answered "b" but then root changes to "b": child hides, so grandchild must hide too.
    const v = visibility(tree, { root: { optionIds: ['b'] }, child: { optionIds: ['b'] } });
    expect(v.get('child')).toBe(false);
    expect(v.get('grandchild')).toBe(false);
    expect(v.get('any')).toBe(true); // root=b
  });

  it('treats unknown references and cycles as false', () => {
    const v = visibility(tree, { cycleA: { text: 'x' }, cycleB: { text: 'y' } });
    expect(v.get('unknown')).toBe(false);
    expect(v.get('cycleA')).toBe(false);
    expect(v.get('cycleB')).toBe(false);
  });

  it('empty all is true, empty any is false', () => {
    const t: QuestionTree = { title: '', questions: [q('x', { showIf: { all: [] } }), q('y', { showIf: { any: [] } })] };
    const v = visibility(t, {});
    expect(v.get('x')).toBe(true);
    expect(v.get('y')).toBe(false);
  });
});

describe('isAnswered', () => {
  it('needs yes or a correction for confirm', () => {
    const c: Question = { id: 'c', kind: 'confirm', title: 'c' };
    expect(isAnswered(c, { optionIds: ['yes'] })).toBe(true);
    expect(isAnswered(c, { optionIds: ['no'] })).toBe(false);
    expect(isAnswered(c, { text: 'fix X' })).toBe(true);
    expect(isAnswered(c, { text: '   ' })).toBe(false);
  });
  it('accepts an Other text for choice questions', () => {
    expect(isAnswered(q('s'), { text: 'something else' })).toBe(true);
    expect(isAnswered(q('s'), { optionIds: [] })).toBe(false);
    expect(isAnswered(q('s'), undefined)).toBe(false);
  });
});

describe('progress', () => {
  const tree: QuestionTree = {
    title: '',
    questions: [q('a'), q('b', { showIf: { question: 'a', anyOf: ['a'] } }), { id: 't', kind: 'text', title: 't' }],
  };
  it('counts only visible questions and ignores answers to hidden ones', () => {
    expect(progress(tree, {})).toMatchObject({ answered: 0, total: 2, complete: false });
    expect(progress(tree, { a: { optionIds: ['b'] }, b: { optionIds: ['a'] }, t: { text: 'x' } })).toMatchObject({
      answered: 2,
      total: 2,
      complete: true,
    });
    expect(progress(tree, { a: { optionIds: ['a'] }, t: { text: 'x' } })).toMatchObject({ answered: 2, total: 3, complete: false });
  });
});
