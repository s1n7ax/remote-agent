// In-memory mock of the API for development and screenshots.
// Enabled by `?mock` in the URL or VITE_MOCK=1.

import { ApiError, setArtifactResolver, setEventSource, setTransport, type Transport } from './api';
import { progress } from './questions';
import type {
  Account,
  AgentKind,
  Answer,
  AppState,
  BenchmarkResult,
  EventKind,
  Issue,
  NotifyEvent,
  PreviewInfo,
  Project,
  Proof,
  Prototype,
  PullRequest,
  Round,
  Run,
  RunEvent,
  Schedule,
  ServerEvent,
  SkillSummary,
  Status,
  Ticket,
  TicketLink,
} from './types';
import { STATUSES } from './types';

/** Reports whether mock mode is requested. */
export function mockRequested(): boolean {
  if (import.meta.env.VITE_MOCK === '1') return true;
  return typeof location !== 'undefined' && new URLSearchParams(location.search).has('mock');
}

const now = Date.now();
const ago = (min: number) => new Date(now - min * 60_000).toISOString();
const ahead = (min: number) => new Date(now + min * 60_000).toISOString();

const models: Partial<Record<AgentKind, string[]>> = {
  claude: ['opus', 'sonnet', 'haiku', 'fable'],
  pi: ['anthropic/claude-sonnet-latest', 'deepseek/deepseek-v4.1-flash', 'openai/gpt-5.5', 'google/gemini-3-pro'],
};
const efforts: Partial<Record<AgentKind, string[]>> = {
  claude: ['low', 'medium', 'high', 'xhigh', 'max'],
  pi: ['off', 'low', 'medium', 'high', 'xhigh'],
};

const projects: Project[] = [
  {
    id: 1,
    name: 'remote-agent',
    repo: 's1n7ax/remote-agent',
    defaultBranch: 'main',
    agent: 'claude',
    model: 'opus',
    effort: 'high',
    createdAt: ago(60 * 24 * 9),
  },
  {
    id: 2,
    name: 'nixos-config',
    repo: 's1n7ax/nixos-config',
    defaultBranch: 'main',
    agent: 'pi',
    model: 'deepseek/deepseek-v4.1-flash',
    effort: 'medium',
    createdAt: ago(60 * 24 * 30),
  },
];

const accounts: Account[] = [
  { id: 1, label: 'Max — personal', priority: 0, warning: true, utilization: 0.82, createdAt: ago(60 * 24 * 20) },
  {
    id: 2,
    label: 'Pro — spare',
    priority: 1,
    warning: false,
    utilization: 1,
    exhaustedTill: ahead(134),
    createdAt: ago(60 * 24 * 12),
  },
];

const settings = {
  maxParallelRuns: 2,
  dailyCapUsd: 5,
  ticketCapUsd: 1.5,
  spentTodayUsd: 1.18,
  openrouterConfigured: true,
  piModels: null as string[] | null,
};

let preview: PreviewInfo | null = null;

/** Placeholder VAPID key: mock mode cannot really subscribe to push. */
const MOCK_VAPID = 'B' + 'A'.repeat(86);

function ticket(p: Partial<Ticket> & Pick<Ticket, 'id' | 'title' | 'status' | 'stage'>): Ticket {
  return {
    projectId: 1,
    kind: 'ticket',
    idea: p.title,
    agent: 'claude',
    model: 'opus',
    effort: 'high',
    prototypes: false,
    fixRounds: 0,
    spendUsd: 0,
    spendCapUsd: 1.5,
    createdAt: ago(600),
    updatedAt: ago(20),
    ...p,
  };
}

const tickets: Ticket[] = [
  ticket({
    id: 101,
    title: 'Swipeable board columns on phones',
    idea: 'The board is unusable at 360px — columns should swipe like tabs and show counts. Maybe try a couple of layouts first?',
    status: 'needs_input',
    stage: 'design',
    prototypes: true,
    issue: 41,
    branch: 'ra/101-swipe-board',
    requirement:
      '# Swipeable board columns\n\nOn phones (≤ 480px) the board shows **one column at a time**; the user swipes between columns or taps a tab.\n\n## Acceptance criteria\n\n- Tabs show each column’s ticket count.\n- The column with open questions is marked.\n- Desktop keeps side-by-side columns.',
    decisions:
      '- **CSS scroll-snap, no gesture library** — native momentum, zero JS on the hot path. Decided by: agent.\n- **Tabs sync from scroll position** via `IntersectionObserver`. Decided by: agent.',
  }),
  ticket({
    id: 102,
    title: 'Retry pushes when GitHub returns 502',
    status: 'working',
    stage: 'build',
    issue: 44,
    branch: 'ra/102-push-retry',
    effort: 'medium',
    model: 'sonnet',
    spendUsd: 0,
  }),
  ticket({ id: 103, title: 'Account usage bar in settings', status: 'backlog', stage: 'explore', agent: 'pi', model: 'deepseek/deepseek-v4.1-flash', effort: 'medium' }),
  ticket({
    id: 104,
    title: 'Export run log as markdown',
    status: 'backlog',
    stage: 'explore',
    startAt: ahead(60 * 9),
  }),
  ticket({ id: 105, title: 'Dark-mode contrast audit', status: 'ready', stage: 'explore', model: 'sonnet', effort: 'low' }),
  ticket({
    id: 106,
    title: 'Rotate VAPID keys from settings',
    status: 'waiting',
    stage: 'build',
    issue: 39,
    waitUntil: ahead(134),
    statusNote: 'All Claude accounts are at their limit.',
  }),
  ticket({
    id: 107,
    title: 'Show CI check names on cards',
    status: 'in_review',
    stage: 'verify',
    issue: 36,
    pr: 52,
    branch: 'ra/107-ci-names',
  }),
  ticket({
    id: 108,
    title: 'Fix flaky SSE reconnect test',
    status: 'failed',
    stage: 'fix',
    issue: 33,
    pr: 49,
    fixRounds: 2,
    statusNote: 'CI still red after 2 fix rounds: e2e/smoke.spec.ts › board loads timed out.',
  }),
  ticket({ id: 109, title: 'Initial board view', status: 'done', stage: 'merged', issue: 12, pr: 20 }),
  ticket({
    id: 110,
    title: 'Nightly: resolve PR merge conflicts',
    kind: 'job',
    status: 'done',
    stage: 'job',
    jobPrompt: 'Rebase open PRs with conflicts.',
  }),
  ticket({
    id: 111,
    title: 'Pi spend cap reached on lint cleanup',
    status: 'needs_input',
    stage: 'build',
    agent: 'pi',
    model: 'openai/gpt-5.5',
    effort: 'low',
    spendUsd: 1.52,
    spendCapUsd: 1.5,
  }),
  ticket({
    id: 201,
    projectId: 2,
    title: 'Move fish abbreviations into a module',
    status: 'backlog',
    stage: 'explore',
    agent: 'pi',
    model: 'deepseek/deepseek-v4.1-flash',
    effort: 'medium',
  }),
];

const round1: Round = {
  ticketId: 101,
  n: 1,
  purpose: 'requirement',
  stage: 'design',
  createdAt: ago(300),
  completedAt: ago(180),
  tree: {
    title: 'Requirement',
    intro: 'I read `web/src/views/Board.svelte`. Columns are fixed at 300px and overflow, so phones see 1.2 columns.',
    questions: [
      {
        id: 'r1',
        kind: 'single',
        title: 'Which screens must the swipe work on?',
        options: [
          { id: 'phone', label: 'Phones only', recommended: true },
          { id: 'tablet', label: 'Phones and tablets' },
        ],
      },
      {
        id: 'r2',
        kind: 'multi',
        title: 'What must a column tab show?',
        options: [
          { id: 'count', label: 'Ticket count' },
          { id: 'attention', label: 'Attention mark for open questions' },
          { id: 'color', label: 'Status color' },
        ],
      },
      { id: 'r3', kind: 'text', title: 'Anything that must not change?' },
    ],
  },
  answers: {
    r1: { optionIds: ['phone'] },
    r2: { optionIds: ['count', 'attention'] },
    r3: { text: 'Desktop layout stays as is.' },
  },
};

const round2: Round = {
  ticketId: 101,
  n: 2,
  purpose: 'design',
  stage: 'build',
  createdAt: ago(25),
  tree: {
    title: 'Design decisions',
    intro:
      'The requirement is on the **Requirement** tab. I built two prototypes — both previews run one at a time on the preview port.',
    questions: [
      {
        id: 'confirm',
        kind: 'confirm',
        title: 'Is the requirement right?',
        context: 'One column at a time below 480px, tabs with counts and an attention mark, desktop unchanged.',
      },
      {
        id: 'proto',
        kind: 'single',
        title: 'Which prototype ships?',
        context: 'Open a preview to try it on your phone.',
        options: [
          {
            id: 'a',
            label: 'A — Tabs on top',
            description: 'Scrollable tab strip under the top bar; swipe the column body.',
            recommended: true,
            preview: { image: 'proto-a', url: 'https://agent.tailnet.ts.net:8443/?p=a' },
          },
          {
            id: 'b',
            label: 'B — Bottom dock',
            description: 'Column switcher docked above the bottom nav, within thumb reach.',
            preview: {
              image: 'proto-b',
              url: 'https://agent.tailnet.ts.net:8443/?p=b',
              markdown: 'Needs the bottom nav to shrink to icons only.',
            },
          },
        ],
      },
      {
        id: 'snap',
        kind: 'single',
        title: 'How should a swipe settle?',
        options: [
          { id: 'snap', label: 'Snap to the next column', description: 'CSS scroll-snap, native momentum.', recommended: true },
          { id: 'free', label: 'Free scroll', description: 'No snapping; columns can sit half-visible.' },
        ],
      },
      {
        id: 'marks',
        kind: 'multi',
        title: 'Which marks go on the tabs?',
        showIf: { question: 'snap', anyOf: ['snap'] },
        options: [
          { id: 'count', label: 'Count', recommended: true },
          { id: 'dot', label: 'Status dot' },
          { id: 'pulse', label: 'Pulse when questions are waiting' },
        ],
      },
      {
        id: 'pulse',
        kind: 'single',
        title: 'How long should the pulse run?',
        showIf: { question: 'marks', anyOf: ['pulse'] },
        allowOther: false,
        options: [
          { id: 'once', label: 'Once, on arrival', recommended: true },
          { id: 'loop', label: 'Until the tab is opened' },
        ],
      },
      {
        id: 'free-why',
        kind: 'text',
        title: 'What should free scroll make easier?',
        showIf: { all: [{ question: 'snap', anyOf: ['free'] }, { not: { question: 'proto', anyOf: ['b'] } }] },
      },
      {
        id: 'dock-size',
        kind: 'single',
        title: 'Dock height',
        showIf: { any: [{ question: 'proto', anyOf: ['b'] }, { question: 'pulse', answered: true }] },
        options: [
          { id: 'compact', label: 'Compact (44px)' },
          { id: 'roomy', label: 'Roomy (56px)', recommended: true },
        ],
      },
    ],
  },
  answers: { snap: { optionIds: ['snap'] } },
};

const round111: Round = {
  ticketId: 111,
  n: 1,
  purpose: 'spend_cap',
  stage: 'build',
  createdAt: ago(8),
  tree: {
    title: 'Spend cap reached',
    intro: 'The run spent **$1.52** of its **$1.50** cap and was paused.',
    questions: [
      {
        id: 'cap',
        kind: 'single',
        title: 'How should this ticket continue?',
        allowOther: false,
        options: [
          { id: 'raise', label: 'Raise the cap by $1 and continue', recommended: true },
          { id: 'claude', label: 'Switch to Claude and continue' },
          { id: 'stop', label: 'Stop here' },
        ],
      },
    ],
  },
  answers: {},
};

const rounds: Round[] = [round1, round2, round111];

const runs: Run[] = [
  { id: 900, ticketId: 101, stage: 'explore', agent: 'claude', model: 'opus', effort: 'high', status: 'succeeded', costUsd: 0, startedAt: ago(320), finishedAt: ago(300), createdAt: ago(320), summary: 'Posted round 1.' },
  { id: 901, ticketId: 101, stage: 'design', agent: 'claude', model: 'opus', effort: 'high', status: 'succeeded', costUsd: 0, startedAt: ago(170), finishedAt: ago(25), createdAt: ago(170), summary: 'Posted round 2 with 2 prototypes.' },
  { id: 910, ticketId: 102, stage: 'build', agent: 'claude', model: 'sonnet', effort: 'medium', status: 'running', costUsd: 0, accountId: 1, startedAt: ago(14), createdAt: ago(14) },
  { id: 920, ticketId: 108, stage: 'fix', agent: 'claude', model: 'opus', effort: 'high', status: 'failed', costUsd: 0, startedAt: ago(90), finishedAt: ago(70), createdAt: ago(90), error: 'CI still red after 2 fix rounds' },
  { id: 930, ticketId: 107, stage: 'verify', agent: 'claude', model: 'opus', effort: 'high', status: 'succeeded', costUsd: 0, startedAt: ago(60), finishedAt: ago(40), createdAt: ago(60) },
];

let eventId = 5000;
const runEvents = new Map<number, RunEvent[]>();
function ev(runId: number, kind: EventKind, text: string, minAgo: number, data?: unknown): RunEvent {
  return { id: ++eventId, runId, kind, text, data, createdAt: ago(minAgo) };
}
runEvents.set(910, []);
for (let i = 1; i <= 6; i++) runEvents.get(910)!.push(ev(910, 'system', `Earlier log line ${i}`, 21 - i));
runEvents.get(910)!.push(
  ev(910, 'system', 'Run started on account “Max — personal” (sonnet, medium).', 14),
  ev(910, 'thinking', 'The push helper wraps git over HTTPS. 502s from GitHub are transient — retry with backoff, but never on 4xx.', 13.8),
  ev(910, 'tool_call', 'Read internal/github/push.go', 13.6, { name: 'Read', input: { file_path: 'internal/github/push.go' } }),
  ev(910, 'tool_result', 'package github\n\nfunc (c *Client) Push(ctx context.Context, dir, branch string) error {\n\treturn c.git(ctx, dir, "push", "origin", branch)\n}', 13.5),
  ev(910, 'text', 'Push has no retry. I will add a bounded exponential backoff (3 attempts) for 5xx and network errors.', 13),
  ev(910, 'tool_call', 'Edit internal/github/push.go', 12, { name: 'Edit' }),
  ev(910, 'tool_result', 'ok', 11.9),
  ev(910, 'user', 'Keep the max wait under 10s please.', 9),
  ev(910, 'text', 'Understood — capping backoff at 1s, 2s, 4s (7s total).', 8.8),
  ev(910, 'tool_call', 'Bash go test ./internal/github/...', 6, { name: 'Bash', input: { command: 'go test ./internal/github/...' } }),
  ev(910, 'error', 'FAIL TestPushRetry (0.01s): expected 3 attempts, got 1', 5.5),
  ev(910, 'text', 'The fake transport returned 502 as a non-error status. Fixing the test double.', 5),
);
runEvents.set(901, [
  ev(901, 'system', 'Run started (opus, high).', 170),
  ev(901, 'text', 'Building prototype A on ra/101-proto-a and B on ra/101-proto-b.', 160),
  ev(901, 'tool_call', 'start_preview pnpm dev --port 5173', 60, { name: 'mcp__app__start_preview' }),
  ev(901, 'system', 'Round 2 posted. Run finished.', 25),
]);
runEvents.forEach((list) => list.sort((a, b) => a.id - b.id));

const proofs: Record<number, Proof> = {
  107: {
    ticketId: 107,
    runId: 930,
    verdict: 'PASS',
    createdAt: ago(40),
    files: ['shot-board', 'shot-card'],
    report:
      '## Verify report\n\n| Check | Result |\n| --- | --- |\n| Card shows check names | pass |\n| Long names truncate at 2 lines | pass |\n| Dark theme contrast ≥ 4.5:1 | pass |\n\nRan `npm run check`, `npm test`, `go test ./...` — all green.\n\nScreenshots taken at 390×844 against the fake GitHub.',
  },
};

const prototypes: Record<number, Prototype[]> = {
  101: [
    { name: 'A — Tabs on top', branch: 'ra/101-proto-a', description: 'Tab strip under the top bar.', artifacts: ['proto-a'], preview: { command: 'npm run dev -- --port 5173', port: 5173 } },
    { name: 'B — Bottom dock', branch: 'ra/101-proto-b', description: 'Switcher docked above the nav.', artifacts: ['proto-b'], preview: { command: 'npm run dev -- --port 5174', port: 5174 } },
  ],
};

const check = (id: number, name: string, status: string, conclusion = '') => ({ id, name, status, conclusion, url: '#' });
const pulls: PullRequest[] = [
  {
    number: 53,
    title: 'build(deps): bump vite from 8.3.7 to 8.3.8',
    url: 'https://github.com/s1n7ax/remote-agent/pull/53',
    state: 'open',
    merged: false,
    draft: false,
    branch: 'deps/vite',
    mergeableState: 'unstable',
    headSha: 'c0ffee1',
    ci: 'pending',
    checks: [check(1, 'go / test', 'completed', 'success'), check(2, 'web / check + build', 'in_progress')],
  },
  {
    number: 52,
    title: 'Show CI check names on cards',
    url: 'https://github.com/s1n7ax/remote-agent/pull/52',
    state: 'open',
    merged: false,
    draft: false,
    branch: 'ra/107-ci-names',
    mergeableState: 'clean',
    headSha: 'a1b2c3d',
    ci: 'success',
    checks: [
      check(3, 'go / test', 'completed', 'success'),
      check(4, 'web / check + build', 'completed', 'success'),
      check(5, 'nix flake check', 'completed', 'success'),
      check(6, 'e2e smoke', 'completed', 'success'),
    ],
  },
  {
    number: 49,
    title: 'Fix flaky SSE reconnect test',
    url: 'https://github.com/s1n7ax/remote-agent/pull/49',
    state: 'open',
    merged: false,
    draft: true,
    branch: 'ra/108-sse-flake',
    mergeableState: 'dirty',
    headSha: 'deadbee',
    ci: 'failure',
    checks: [check(7, 'go / test', 'completed', 'success'), check(8, 'e2e smoke', 'completed', 'failure')],
  },
];

const issues: Issue[] = [
  { number: 44, title: 'Retry pushes when GitHub returns 502', url: '#', author: 's1n7ax', labels: ['bug'] },
  { number: 41, title: 'Swipeable board columns on phones', url: '#', author: 's1n7ax', labels: ['ui'] },
  { number: 39, title: 'Rotate VAPID keys from settings', url: '#', author: 's1n7ax', labels: [] },
  { number: 30, title: 'Document the tailscale serve setup', url: '#', author: 'someone-else', labels: ['docs'] },
];

let scheduleId = 3;
const schedules: Schedule[] = [
  { id: 1, projectId: 1, name: 'Fix merge conflicts', cron: '0 9 * * *', prompt: 'Rebase every open PR that has merge conflicts onto main and push.', agent: 'claude', model: 'sonnet', effort: 'medium', enabled: true, lastRunAt: ago(60 * 5), nextRunAt: ahead(60 * 19), createdAt: ago(60 * 24 * 7) },
  { id: 2, projectId: 1, name: 'Weekly dependency notes', cron: '0 8 * * 1', prompt: 'Summarise open dependency PRs and their CI status as an issue comment.', agent: 'pi', model: 'deepseek/deepseek-v4.1-flash', effort: 'low', enabled: false, createdAt: ago(60 * 24 * 7) },
];

const skillFiles: Record<string, Record<string, string>> = {
  questions: {
    'SKILL.md': '---\nname: questions\ndescription: Ask batched, branching question trees.\n---\n\n# Questions\n\nPost every question of a round at once with `submit_questions`.\nUse `showIf` for follow-ups instead of asking later.\n',
    'evals/evals.json': '[\n  { "prompt": "Add dark mode", "expect": "asks about default theme" }\n]\n',
  },
  ship: {
    'SKILL.md': '---\nname: ship\ndescription: Event-driven /ship stages.\n---\n\n# Ship\n\nEach stage is a separate run. Call `stage_done` when finished.\n',
  },
  verify: { 'SKILL.md': '---\nname: verify\ndescription: Prove a change works.\n---\n\n# Verify\n' },
};
const skillMeta: Record<string, { description: string; hasEvals: boolean }> = {
  questions: { description: 'Ask batched, branching question trees.', hasEvals: true },
  ship: { description: 'Event-driven /ship stages run by separate agents.', hasEvals: false },
  verify: { description: 'Final validation with a screenshot report.', hasEvals: true },
};

const notify: Record<string, boolean> = {
  questions_waiting: true,
  pr_ready: true,
  run_failed: true,
  run_paused: true,
  usage_limited: false,
  job_done: false,
  merged: false,
};

// ---------- event bus ----------

const sinks = new Set<(e: ServerEvent) => void>();
function emit(e: ServerEvent) {
  sinks.forEach((s) => s(e));
}

function touch(t: Ticket) {
  t.updatedAt = new Date().toISOString();
  emit({ type: 'ticket', projectId: t.projectId, ticketId: t.id, data: t });
}

const liveLines: [EventKind, string][] = [
  ['tool_call', 'Bash go test ./internal/github/...'],
  ['tool_result', 'ok  github.com/s1n7ax/remote-agent/internal/github 0.41s'],
  ['thinking', 'Tests pass. Next: run the race detector before pushing.'],
  ['tool_call', 'Bash go test -race ./...'],
  ['tool_result', 'ok  (14 packages)'],
  ['text', 'All green. Pushing the branch and opening a draft PR.'],
  ['tool_call', 'mcp__app__push_branch'],
  ['system', 'Pushed ra/102-push-retry.'],
];

// ---------- routing ----------

function find<T>(xs: T[], pred: (x: T) => boolean, what: string): T {
  const x = xs.find(pred);
  if (!x) throw new ApiError(404, `${what} not found`);
  return x;
}

// Stored links: `from` depends on `to`, or the two are related.
type MockLink = { id: number; from: number; to: number; kind: 'depends_on' | 'related' };
const links: MockLink[] = [];
let nextLink = 1;

function linksOf(id: number): TicketLink[] {
  return links
    .filter((l) => l.from === id || l.to === id)
    .map((l) => {
      const otherId = l.from === id ? l.to : l.from;
      const o = tickets.find((x) => x.id === otherId);
      const kind = l.from === id || l.kind === 'related' ? l.kind : 'blocks';
      return o
        ? { id: l.id, kind, ticketId: o.id, title: o.title, issue: o.issue, status: o.status, statusNote: o.statusNote }
        : { id: l.id, kind, ticketId: otherId, title: `ticket ${otherId}`, deleted: true };
    });
}

function addMockLink(id: number, to: number, kind: string) {
  const t = find(tickets, (x) => x.id === id, 'ticket');
  const o = tickets.find((x) => x.id === to);
  if (!o) throw new ApiError(400, `invalid request: ticket ${to} does not exist`);
  if (to === id) throw new ApiError(400, 'invalid request: a ticket cannot depend on itself');
  if (o.projectId !== t.projectId) throw new ApiError(400, 'invalid request: links are only between tickets of the same project');
  if (o.kind === 'job' || t.kind === 'job') throw new ApiError(400, 'invalid request: jobs (scheduled runs) cannot have links');
  if (kind === 'related') {
    links.push({ id: nextLink++, from: id, to, kind: 'related' });
    return;
  }
  const [from, dep] = kind === 'blocks' ? [to, id] : [id, to];
  const reach = (a: number, seen = new Set<number>()): boolean =>
    a === from || links.some((l) => l.kind === 'depends_on' && l.from === a && !seen.has(l.to) && (seen.add(l.to), reach(l.to, seen)));
  if (reach(dep)) throw new ApiError(400, 'invalid request: that would make a dependency loop');
  links.push({ id: nextLink++, from, to: dep, kind: 'depends_on' });
}

function detail(id: number) {
  const t = find(tickets, (x) => x.id === id, 'ticket');
  const out: Record<string, unknown> = {
    ticket: t,
    rounds: rounds.filter((r) => r.ticketId === id).sort((a, b) => a.n - b.n),
    runs: runs.filter((r) => r.ticketId === id),
    prototypes: prototypes[id] ?? [],
    links: linksOf(id),
  };
  if (proofs[id]) out.proof = proofs[id];
  if (preview?.ticketId === id) out.preview = preview;
  return out;
}

function activeRun(id: number): Run | undefined {
  return runs.filter((r) => r.ticketId === id && ['queued', 'running', 'paused'].includes(r.status)).at(-1);
}

type Handler = (m: RegExpMatchArray, body: any, query: URLSearchParams) => unknown;
const routes: [string, RegExp, Handler][] = [];
const on = (method: string, re: string, h: Handler) => routes.push([method, new RegExp(`^${re}$`), h]);

on('GET', '/api/state', (): AppState => ({
  version: 'mock',
  projects,
  accounts: [...accounts].sort((a, b) => a.priority - b.priority),
  settings,
  models,
  efforts,
  vapidPublicKey: MOCK_VAPID,
  preview,
  activeRuns: runs.filter((r) => r.status === 'running').length,
  draining: false,
  statuses: [...STATUSES],
  notifyEvents: { questions_waiting: true, pr_ready: true, run_failed: true, run_paused: true, usage_limited: false, job_done: false, merged: false },
}));
on('GET', '/api/projects', () => projects);
on('POST', '/api/projects', (_m, b) => {
  const repo = String(b.repo).trim();
  if (!/^[^/]+\/[^/]+$/.test(repo)) throw new ApiError(400, 'repo must be owner/name');
  const p: Project = {
    id: Math.max(...projects.map((x) => x.id)) + 1,
    name: b.name || repo.split('/')[1],
    repo,
    defaultBranch: 'main',
    agent: b.agent || 'claude',
    model: b.model,
    effort: b.effort,
    createdAt: new Date().toISOString(),
  };
  projects.push(p);
  return p;
});
on('PATCH', '/api/projects/(\\d+)', (m, b) => Object.assign(find(projects, (p) => p.id === +m[1], 'project'), b));
on('DELETE', '/api/projects/(\\d+)', (m) => {
  projects.splice(projects.findIndex((p) => p.id === +m[1]), 1);
  return { deleted: true };
});
on('GET', '/api/projects/(\\d+)/board', (m) => {
  const projectId = +m[1];
  const columns = Object.fromEntries(STATUSES.map((s) => [s, [] as Ticket[]])) as Record<Status, Ticket[]>;
  tickets
    .filter((t) => t.projectId === projectId)
    .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
    .forEach((t) => columns[t.status].push(t));
  return { projectId, columns };
});
on('POST', '/api/projects/(\\d+)/tickets', (m, b) => {
  if (!String(b.idea ?? '').trim()) throw new ApiError(400, 'invalid request: an idea is required');
  const t = ticket({
    id: Math.max(...tickets.map((x) => x.id)) + 1,
    projectId: +m[1],
    title: b.title || String(b.idea).split('\n')[0].slice(0, 72),
    idea: b.idea,
    status: b.start ? 'ready' : 'backlog',
    stage: 'explore',
    agent: b.agent,
    model: b.model,
    effort: b.effort,
    prototypes: b.prototypes,
    startAt: b.startAt,
    createdAt: new Date().toISOString(),
  });
  tickets.push(t);
  for (const to of b.dependsOn ?? []) addMockLink(t.id, to, 'depends_on');
  for (const to of b.related ?? []) addMockLink(t.id, to, 'related');
  emit({ type: 'board', projectId: t.projectId });
  return t;
});
on('POST', '/api/tickets/(\\d+)/links', (m, b) => {
  addMockLink(+m[1], +b.to, b.kind);
  return { links: linksOf(+m[1]) };
});
on('DELETE', '/api/tickets/(\\d+)/links/(\\d+)', (m) => {
  const i = links.findIndex((l) => l.id === +m[2]);
  if (i < 0) throw new ApiError(404, 'link not found');
  links.splice(i, 1);
  return { links: linksOf(+m[1]) };
});
on('GET', '/api/tickets/(\\d+)', (m) => detail(+m[1]));
on('PATCH', '/api/tickets/(\\d+)', (m, b) => {
  const t = find(tickets, (x) => x.id === +m[1], 'ticket');
  const { clearStartAt, ...rest } = b;
  Object.assign(t, rest);
  if (clearStartAt) delete t.startAt;
  touch(t);
  return t;
});
on('DELETE', '/api/tickets/(\\d+)', (m) => {
  const i = tickets.findIndex((x) => x.id === +m[1]);
  const [t] = tickets.splice(i, 1);
  emit({ type: 'board', projectId: t.projectId });
  return { deleted: true };
});
on('POST', '/api/tickets/(\\d+)/(start|pause|resume|stop|retry)', (m) => {
  const t = find(tickets, (x) => x.id === +m[1], 'ticket');
  const run = activeRun(t.id);
  switch (m[2]) {
    case 'start':
      t.status = 'ready';
      delete t.startAt;
      break;
    case 'pause':
      if (!run) throw new ApiError(409, 'no active run');
      run.status = 'paused';
      t.statusNote = 'Paused by you.';
      break;
    case 'resume':
      if (!run) throw new ApiError(409, 'no active run');
      run.status = 'running';
      delete t.statusNote;
      break;
    case 'stop':
      if (run) run.status = 'stopped';
      t.status = 'backlog';
      t.statusNote = 'Stopped by you.';
      break;
    case 'retry':
      t.status = 'ready';
      t.fixRounds = 0;
      delete t.statusNote;
      break;
  }
  touch(t);
  return t;
});
on('PUT', '/api/tickets/(\\d+)/rounds/(\\d+)/answers/([^/]+)', (m, b: Answer) => {
  const t = find(tickets, (x) => x.id === +m[1], 'ticket');
  const r = find(rounds, (x) => x.ticketId === t.id && x.n === +m[2], 'round');
  if (r.completedAt) throw new ApiError(400, `invalid request: round ${r.n} is already complete`);
  const qid = decodeURIComponent(m[3]);
  if (!r.tree.questions.some((q) => q.id === qid)) throw new ApiError(404, 'question not found');
  if (!b.optionIds?.length && !b.text) delete r.answers[qid];
  else r.answers[qid] = b;
  const completed = progress(r.tree, r.answers).complete;
  if (completed) {
    r.completedAt = new Date().toISOString();
    t.status = 'ready';
    t.stage = r.stage;
  }
  touch(t);
  return { round: r, completed, ticket: t };
});
on('POST', '/api/tickets/(\\d+)/message', (m, b) => {
  const run = activeRun(+m[1]);
  if (!run) throw new ApiError(409, 'no active run');
  const e = ev(run.id, 'user', b.text, 0);
  runEvents.get(run.id)?.push(e);
  emit({ type: 'run_event', projectId: 1, ticketId: +m[1], data: e });
  return { sent: true };
});
on('GET', '/api/tickets/(\\d+)/runs', (m) => runs.filter((r) => r.ticketId === +m[1]));
on('GET', '/api/tickets/(\\d+)/runs/(\\d+)/events', (m, _b, q) => {
  const list = runEvents.get(+m[2]) ?? [];
  const after = +(q.get('after') ?? 0);
  const limit = +(q.get('limit') ?? 500);
  return list.filter((e) => e.id > after).slice(0, limit);
});
on('POST', '/api/tickets/(\\d+)/preview', (m, b) => {
  const p = (prototypes[+m[1]] ?? []).find((x) => x.name === b.prototype);
  if (!p) throw new ApiError(404, 'prototype not found');
  preview = { ticketId: +m[1], branch: p.branch, url: 'https://agent.tailnet.ts.net:8443/' };
  return preview;
});
on('DELETE', '/api/preview', () => {
  preview = null;
  return { stopped: true };
});
on('GET', '/api/projects/(\\d+)/github', (m) => {
  const byPr: Record<string, number> = {};
  tickets.filter((t) => t.projectId === +m[1] && t.pr).forEach((t) => (byPr[String(t.pr)] = t.id));
  const p = find(projects, (x) => x.id === +m[1], 'project');
  return +m[1] === 1 ? { repo: p.repo, pulls, issues, ticketsByPr: byPr } : { repo: p.repo, pulls: [], issues: [], ticketsByPr: {} };
});
on('POST', '/api/projects/(\\d+)/pulls/(\\d+)/merge', (m) => {
  const pr = find(pulls, (p) => p.number === +m[2], 'pull request');
  if (pr.draft) throw new ApiError(500, 'merge #' + pr.number + ': Pull Request is still a draft');
  pr.state = 'closed';
  pr.merged = true;
  const t = tickets.find((x) => x.pr === pr.number);
  if (t) {
    t.status = 'done';
    t.stage = 'merged';
    touch(t);
  }
  return { merged: true };
});
on('GET', '/api/projects/(\\d+)/schedules', (m) => schedules.filter((s) => s.projectId === +m[1]));
on('POST', '/api/projects/(\\d+)/schedules', (m, b) => {
  const s: Schedule = { ...b, id: ++scheduleId, projectId: +m[1], createdAt: new Date().toISOString(), nextRunAt: ahead(60) };
  schedules.push(s);
  return s;
});
on('PATCH', '/api/schedules/(\\d+)', (m, b) => Object.assign(find(schedules, (s) => s.id === +m[1], 'schedule'), b));
on('DELETE', '/api/schedules/(\\d+)', (m) => {
  schedules.splice(schedules.findIndex((s) => s.id === +m[1]), 1);
  return { deleted: true };
});
on('GET', '/api/accounts', () => [...accounts].sort((a, b) => a.priority - b.priority));
on('POST', '/api/accounts', (_m, b) => {
  if (!b.label || !b.token) throw new ApiError(400, 'an account needs a label and the token from `claude setup-token`');
  const a: Account = { id: Math.max(0, ...accounts.map((x) => x.id)) + 1, label: b.label, priority: b.priority ?? 0, warning: false, utilization: 0, createdAt: new Date().toISOString() };
  accounts.push(a);
  return a;
});
on('PATCH', '/api/accounts/(\\d+)', (m, b) => {
  const a = find(accounts, (x) => x.id === +m[1], 'account');
  if (b.label !== undefined) a.label = b.label;
  if (b.priority !== undefined) a.priority = b.priority;
  if (b.clearLimited) {
    delete a.exhaustedTill;
    a.warning = false;
  }
  return a;
});
on('DELETE', '/api/accounts/(\\d+)', (m) => {
  accounts.splice(accounts.findIndex((a) => a.id === +m[1]), 1);
  return { deleted: true };
});
on('GET', '/api/settings', () => settings);
on('PUT', '/api/settings', (_m, b) => {
  if (b.dailyCapUsd !== undefined) settings.dailyCapUsd = b.dailyCapUsd;
  if (b.ticketCapUsd !== undefined) settings.ticketCapUsd = b.ticketCapUsd;
  if (b.piModels !== undefined) settings.piModels = b.piModels;
  return settings;
});
on('GET', '/api/skills', (): SkillSummary[] =>
  Object.keys(skillFiles).map((name) => ({ name, ...skillMeta[name], files: Object.keys(skillFiles[name]) })),
);
on('GET', '/api/skills/([^/]+)/files/(.+)', (m) => {
  const name = decodeURIComponent(m[1]);
  const path = m[2].split('/').map(decodeURIComponent).join('/');
  const content = skillFiles[name]?.[path];
  if (content === undefined) throw new ApiError(404, 'not found');
  return { content };
});
on('PUT', '/api/skills/([^/]+)/files/(.+)', (m, b) => {
  const name = decodeURIComponent(m[1]);
  const path = m[2].split('/').map(decodeURIComponent).join('/');
  (skillFiles[name] ??= {})[path] = b.content;
  return { commit: Math.random().toString(16).slice(2, 12) };
});
let bench: BenchmarkResult[] = [];
const benchDone = (skill: string, w: number, wo: number): BenchmarkResult => ({
  skill,
  status: 'done',
  with: w,
  without: wo,
  delta: w - wo,
  costUsd: 0,
  cases: [],
  verdict: w - wo > 0.1 ? 'keep' : w >= 0.7 && wo >= 0.7 ? 'remove?' : 'investigate',
  startedAt: ago(30),
  endedAt: ago(20),
});
on('GET', '/api/skills/benchmark', () => bench);
on('POST', '/api/skills/benchmark', () => {
  if (bench.some((r) => r.status === 'running')) throw new ApiError(400, 'invalid: a benchmark is already running');
  const started = Object.keys(skillMeta).filter((n) => skillMeta[n].hasEvals && skillFiles[n]);
  bench = started.map((skill) => ({ skill, status: 'running', with: 0, without: 0, delta: 0, costUsd: 0, cases: null, startedAt: new Date().toISOString() }));
  setTimeout(() => {
    bench = [benchDone('questions', 0.92, 0.41), benchDone('verify', 0.78, 0.74)].filter((r) => started.includes(r.skill));
  }, 6000);
  return { started };
});
const removedSkills: Record<string, Record<string, string>> = {};
on('DELETE', '/api/skills/([^/]+)', (m) => {
  const name = decodeURIComponent(m[1]);
  removedSkills[name] = skillFiles[name];
  delete skillFiles[name];
  return { commit: 'f00dfeed' };
});
on('POST', '/api/skills/([^/]+)/restore', (m) => {
  const name = decodeURIComponent(m[1]);
  if (!removedSkills[name]) throw new ApiError(400, 'invalid: nothing to restore');
  skillFiles[name] = removedSkills[name];
  delete removedSkills[name];
  return { commit: 'feedf00d' };
});

on('GET', '/api/notifications', () => ({ prefs: notify, vapidPublicKey: MOCK_VAPID }));
on('PUT', '/api/notifications', (_m, b) => {
  Object.assign(notify, b.prefs);
  return { prefs: notify, vapidPublicKey: MOCK_VAPID };
});
on('POST', '/api/push/subscribe', () => ({ subscribed: true }));
on('POST', '/api/push/test', () => ({ sent: true }));

/** Answers requests from in-memory data. */
export const mockTransport: Transport = async (method, path, body) => {
  await new Promise((r) => setTimeout(r, 80));
  const [p, qs] = path.split('?');
  for (const [m, re, h] of routes) {
    if (m !== method) continue;
    const match = p.match(re);
    if (match) {
      const out = await h(match, body === undefined ? undefined : structuredClone(body), new URLSearchParams(qs));
      return out === undefined ? undefined : structuredClone(out);
    }
  }
  throw new ApiError(404, `mock: no route for ${method} ${p}`);
};

// ---------- artifacts ----------

function shot(title: string, hue: number, layout: 'top' | 'dock' | 'board' | 'card'): string {
  const cols = [0, 1, 2]
    .map(
      (i) =>
        `<rect x="${18 + i * 6}" y="${(layout === 'top' ? 118 : 70) + i * 92}" width="${354 - i * 12}" height="80" rx="10" fill="hsl(${hue} 30% ${96 - i * 3}%)" stroke="hsl(${hue} 30% 80%)"/>` +
        `<rect x="${32 + i * 6}" y="${(layout === 'top' ? 134 : 86) + i * 92}" width="${180 - i * 20}" height="10" rx="5" fill="hsl(${hue} 20% 40%)"/>` +
        `<rect x="${32 + i * 6}" y="${(layout === 'top' ? 154 : 106) + i * 92}" width="60" height="16" rx="8" fill="hsl(${hue} 70% 85%)"/>`,
    )
    .join('');
  const tabs =
    layout === 'top'
      ? [0, 1, 2, 3].map((i) => `<rect x="${16 + i * 92}" y="72" width="84" height="28" rx="14" fill="${i === 1 ? `hsl(${hue} 70% 45%)` : `hsl(${hue} 20% 88%)`}"/>`).join('')
      : layout === 'dock'
        ? `<rect x="0" y="700" width="390" height="56" fill="hsl(${hue} 30% 92%)"/>` +
          [0, 1, 2, 3].map((i) => `<rect x="${16 + i * 92}" y="712" width="84" height="32" rx="16" fill="${i === 0 ? `hsl(${hue} 70% 45%)` : '#fff'}"/>`).join('')
        : '';
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="390" height="844" viewBox="0 0 390 844"><rect width="390" height="844" fill="hsl(${hue} 25% 98%)"/><rect width="390" height="56" fill="hsl(${hue} 40% 22%)"/><text x="18" y="35" font-family="sans-serif" font-size="17" fill="#fff">${title}</text>${tabs}${cols}<rect y="772" width="390" height="72" fill="hsl(${hue} 20% 94%)"/></svg>`;
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
}

const artifacts: Record<string, string> = {
  'proto-a': shot('Prototype A — tabs on top', 222, 'top'),
  'proto-b': shot('Prototype B — bottom dock', 160, 'dock'),
  'shot-board': shot('Board — CI names', 222, 'board'),
  'shot-card': shot('Card detail', 30, 'card'),
};

/** Installs the mock transport, event source and artifact URLs. */
export function installMock(): void {
  setTransport(mockTransport);
  setArtifactResolver((id) => artifacts[id] ?? shot(id, 0, 'board'));
  let i = 0;
  setEventSource((onEvent) => {
    sinks.add(onEvent);
    const timer = setInterval(() => {
      const run = runs.find((r) => r.id === 910);
      if (!run || run.status !== 'running') return;
      const [kind, text] = liveLines[i++ % liveLines.length];
      const e: RunEvent = { id: ++eventId, runId: 910, kind, text, createdAt: new Date().toISOString() };
      runEvents.get(910)!.push(e);
      emit({ type: 'run_event', projectId: 1, ticketId: 102, data: e });
    }, 3000);
    return () => {
      clearInterval(timer);
      sinks.delete(onEvent);
    };
  });
}
