// Types mirroring internal/domain/domain.go JSON, plus the API envelopes the
// frontend relies on. Times are RFC 3339 strings.

export type Status =
  | 'backlog'
  | 'needs_input'
  | 'ready'
  | 'working'
  | 'waiting'
  | 'in_review'
  | 'done'
  | 'failed';

export const STATUSES: Status[] = [
  'backlog',
  'needs_input',
  'ready',
  'working',
  'waiting',
  'in_review',
  'done',
  'failed',
];

export const STATUS_LABEL: Record<Status, string> = {
  backlog: 'Backlog',
  needs_input: 'Needs input',
  ready: 'Ready',
  working: 'Working',
  waiting: 'Waiting',
  in_review: 'In review',
  done: 'Done',
  failed: 'Failed',
};

export type Stage = 'explore' | 'design' | 'build' | 'ci' | 'fix' | 'verify' | 'merged' | 'job';
export type Kind = 'ticket' | 'job';
export type AgentKind = 'claude' | 'pi' | 'fake';

export interface Project {
  id: number;
  name: string;
  repo: string;
  defaultBranch: string;
  agent: AgentKind;
  model: string;
  effort: string;
  createdAt: string;
}

export interface Ticket {
  id: number;
  projectId: number;
  kind: Kind;
  title: string;
  idea: string;
  status: Status;
  stage: Stage;
  agent: AgentKind;
  model: string;
  effort: string;
  prototypes: boolean;
  issue?: number;
  branch?: string;
  pr?: number;
  verifiedSha?: string;
  requirement?: string;
  decisions?: string;
  statusNote?: string;
  startAt?: string;
  waitUntil?: string;
  fixRounds: number;
  spendUsd: number;
  spendCapUsd: number;
  jobPrompt?: string;
  createdAt: string;
  updatedAt: string;
}

/** How a linked ticket relates to the ticket it is seen from. */
export type LinkKind = 'depends_on' | 'blocks' | 'related';

export const LINK_LABEL: Record<LinkKind, string> = {
  depends_on: 'Depends on',
  blocks: 'Blocks',
  related: 'Related to',
};

/** One link as seen from one of its tickets; the fields describe the other ticket. */
export interface TicketLink {
  id: number;
  kind: LinkKind;
  ticketId: number;
  title: string;
  issue?: number;
  status?: Status; // absent once deleted
  statusNote?: string;
  deleted?: boolean;
}

export type QuestionKind = 'single' | 'multi' | 'text' | 'confirm';

export interface Preview {
  markdown?: string;
  image?: string; // artifact id
  url?: string; // live preview link
}

export interface Option {
  id: string;
  label: string;
  description?: string;
  recommended?: boolean;
  preview?: Preview;
}

export interface Condition {
  question?: string;
  anyOf?: string[];
  answered?: boolean;
  all?: Condition[];
  any?: Condition[];
  not?: Condition;
}

export interface Question {
  id: string;
  kind: QuestionKind;
  title: string;
  context?: string;
  options?: Option[];
  allowOther?: boolean;
  showIf?: Condition;
}

export type Purpose = 'requirement' | 'design' | 'follow_up' | 'spend_cap' | 'verify';

export interface QuestionTree {
  purpose?: Purpose;
  title: string;
  intro?: string;
  questions: Question[];
}

export interface Answer {
  optionIds?: string[];
  text?: string;
}

export interface Round {
  ticketId: number;
  n: number;
  purpose: Purpose;
  stage: Stage;
  tree: QuestionTree;
  answers: Record<string, Answer>;
  completedAt?: string;
  createdAt: string;
}

export type RunStatus =
  | 'queued'
  | 'running'
  | 'paused'
  | 'succeeded'
  | 'failed'
  | 'stopped'
  | 'limited'
  | 'capped';

export interface Run {
  id: number;
  ticketId: number;
  stage: Stage;
  agent: AgentKind;
  model: string;
  effort: string;
  accountId?: number;
  sessionId?: string;
  status: RunStatus;
  summary?: string;
  error?: string;
  costUsd: number;
  startedAt?: string;
  finishedAt?: string;
  createdAt: string;
}

export type EventKind = 'text' | 'thinking' | 'tool_call' | 'tool_result' | 'user' | 'system' | 'error';

export interface RunEvent {
  id: number;
  runId: number;
  kind: EventKind;
  text: string;
  data?: unknown;
  createdAt: string;
}

export interface Account {
  id: number;
  label: string;
  priority: number;
  exhaustedTill?: string;
  warning: boolean;
  utilization: number;
  createdAt: string;
}

export interface Schedule {
  id: number;
  projectId: number;
  name: string;
  cron: string;
  prompt: string;
  agent: AgentKind;
  model: string;
  effort: string;
  enabled: boolean;
  lastRunAt?: string;
  nextRunAt?: string;
  createdAt: string;
}

export interface Artifact {
  id: string;
  ticketId: number;
  runId: number;
  name: string;
  mime: string;
  size: number;
  createdAt: string;
}

export interface Prototype {
  name: string;
  branch: string;
  description: string;
  artifacts?: string[]; // screenshot artifact ids
  preview?: { command: string; port: number };
}

export interface Proof {
  ticketId: number;
  runId: number;
  verdict: 'PASS' | 'FAIL' | string;
  report: string;
  files: string[];
  createdAt: string;
}

export type NotifyEvent =
  | 'questions_waiting'
  | 'pr_ready'
  | 'run_failed'
  | 'run_paused'
  | 'usage_limited'
  | 'job_done'
  | 'merged';

export const NOTIFY_LABEL: Record<NotifyEvent, string> = {
  questions_waiting: 'Questions waiting for you',
  pr_ready: 'PR ready for review',
  run_failed: 'Run failed',
  run_paused: 'Run paused',
  usage_limited: 'Usage limit hit',
  job_done: 'Scheduled job finished',
  merged: 'PR merged',
};

// ---- API envelopes (internal/api/handlers.go) ----

/** GET/PUT /api/settings. */
export interface Settings {
  maxParallelRuns: number;
  dailyCapUsd: number;
  ticketCapUsd: number;
  spentTodayUsd: number;
  openrouterConfigured: boolean;
  piModels: string[] | null;
}

/** The running live prototype preview (one at a time). */
export interface PreviewInfo {
  ticketId: number;
  branch: string;
  url: string;
}

/** GET /api/state. */
export interface AppState {
  version: string;
  projects: Project[];
  accounts: Account[];
  settings: Settings;
  models: Partial<Record<AgentKind, string[]>>;
  efforts: Partial<Record<AgentKind, string[]>>;
  vapidPublicKey: string;
  preview: PreviewInfo | null;
  activeRuns: number;
  draining: boolean;
  statuses: Status[];
  notifyEvents: Record<NotifyEvent, boolean>;
}

export type CIState = 'pending' | 'success' | 'failure' | 'none';

/** GET /api/projects/{id}/board. */
export interface Board {
  projectId: number;
  columns: Record<Status, Ticket[]>;
}

export interface Check {
  name: string;
  status: 'queued' | 'in_progress' | 'completed' | string;
  conclusion: string; // success | failure | … ('' while running)
  url: string;
  id: number;
}

/** A pull request as the orchestrator sees it (orchestrator.PullState). */
export interface PullRequest {
  number: number;
  title: string;
  branch: string;
  url: string;
  state: 'open' | 'closed';
  merged: boolean;
  draft: boolean;
  mergeableState: string; // clean | dirty | blocked | unstable | unknown …
  headSha: string;
  ci: CIState;
  checks: Check[] | null;
}

export interface Issue {
  number: number;
  title: string;
  url: string;
  labels: string[] | null;
  author: string;
}

/** GET /api/projects/{id}/github. */
export interface GitHubView {
  repo: string;
  pulls: PullRequest[];
  issues: Issue[];
  ticketsByPr: Record<string, number>;
}

/** GET /api/tickets/{id}. */
export interface TicketDetail {
  ticket: Ticket;
  rounds: Round[];
  runs: Run[];
  prototypes: Prototype[];
  links: TicketLink[];
  proof?: Proof;
  preview?: PreviewInfo;
}

export interface AnswerResult {
  round: Round;
  completed: boolean;
  ticket: Ticket;
}

export interface SkillSummary {
  name: string;
  description: string;
  files: string[];
  hasEvals: boolean;
  agents?: boolean;
}

/** One skill's latest ablation benchmark (skills.Result). */
export interface BenchmarkResult {
  skill: string;
  status: 'running' | 'done' | 'failed';
  error?: string;
  with: number;
  without: number;
  delta: number;
  costUsd: number;
  cases: unknown[] | null;
  verdict?: string;
  startedAt: string;
  endedAt?: string;
}

/** GET/PUT /api/notifications. */
export interface NotificationPrefs {
  prefs: Record<string, boolean>;
  vapidPublicKey: string;
}

export interface ProjectInput {
  repo: string;
  name?: string;
  defaultBranch?: string;
  agent: AgentKind;
  model: string;
  effort: string;
}

export interface TicketInput {
  idea: string;
  title?: string;
  agent: AgentKind;
  model: string;
  effort: string;
  prototypes: boolean;
  start?: boolean;
  startAt?: string;
  dependsOn?: number[];
  related?: number[];
}

export interface TicketPatch {
  title?: string;
  agent?: AgentKind;
  model?: string;
  effort?: string;
  prototypes?: boolean;
  startAt?: string;
  clearStartAt?: boolean;
  spendCapUsd?: number;
}

export type ScheduleInput = Pick<Schedule, 'name' | 'cron' | 'prompt' | 'agent' | 'model' | 'effort' | 'enabled'>;

export interface SettingsPatch {
  dailyCapUsd?: number;
  ticketCapUsd?: number;
  piModels?: string[];
}

/** Server-sent events on /api/events: `event: <type>`, data = orchestrator.Message. */
export type ServerEvent =
  | { type: 'ticket'; projectId: number; ticketId: number; data: Ticket }
  | { type: 'run_event'; projectId: number; ticketId: number; data: RunEvent }
  | { type: 'board'; projectId: number; ticketId?: number; data?: unknown }
  | { type: 'links'; projectId: number; ticketId: number };
