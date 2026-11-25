// Global app state loaded from /api/state.

import { api, onUnauthorized } from './api';
import { dropPairing } from './pairing';
import { errorText } from './format';
import type { AgentKind, AppState, Project } from './types';

/** One agent harness the user can pick, with its models and effort levels. */
export interface AgentInfo {
  kind: AgentKind;
  label: string;
  models: { id: string; label: string }[];
  efforts: string[];
}

const AGENT_LABEL: Record<string, string> = { claude: 'Claude', pi: 'Pi', fake: 'Fake' };

/** Short chip label for a model id: "opus" → "Opus", "deepseek/deepseek-v4.1-flash" → "deepseek-v4.1-flash". */
export function modelLabel(id: string): string {
  const base = id.includes('/') ? id.slice(id.lastIndexOf('/') + 1) : id;
  return id.includes('/') ? base : base.charAt(0).toUpperCase() + base.slice(1);
}

class App {
  state = $state<AppState | null>(null);
  error = $state<string | null>(null);
  /** The server wants this browser to sign in with the login token. */
  needsLogin = $state(false);

  constructor() {
    onUnauthorized(() => (this.needsLogin = true));
  }

  async login(token: string): Promise<void> {
    await api.login(token);
    this.needsLogin = false;
    await this.load();
  }
  toast = $state<{ text: string; kind: 'ok' | 'error'; action?: { label: string; run: () => void } } | null>(null);
  #toastTimer: ReturnType<typeof setTimeout> | undefined;

  async load(): Promise<void> {
    try {
      const st = await api.state();
      // Go encodes empty slices as null.
      st.projects ??= [];
      st.accounts ??= [];
      st.models ??= {};
      st.efforts ??= {};
      this.state = st;
      this.error = null;
      dropPairing(); // signed in already: a pairing token is not needed
    } catch (e) {
      if (!this.needsLogin) this.error = errorText(e);
    }
  }

  project(id: number): Project | undefined {
    return this.state?.projects.find((p) => p.id === id);
  }

  agents = $derived.by((): AgentInfo[] => {
    const st = this.state;
    if (!st) return [];
    return (Object.keys(st.models) as AgentKind[]).map((kind) => ({
      kind,
      label: AGENT_LABEL[kind] ?? kind,
      models: (st.models[kind] ?? []).map((id) => ({ id, label: modelLabel(id) })),
      efforts: st.efforts[kind] ?? [],
    }));
  });

  agent(kind: AgentKind): AgentInfo | undefined {
    return this.agents.find((a) => a.kind === kind);
  }

  modelLabel(_kind: AgentKind, model: string): string {
    return modelLabel(model);
  }

  notify(text: string, kind: 'ok' | 'error' = 'ok', action?: { label: string; run: () => void }): void {
    this.toast = { text, kind, action };
    clearTimeout(this.#toastTimer);
    this.#toastTimer = setTimeout(() => (this.toast = null), action ? 8000 : kind === 'error' ? 6000 : 2600);
  }

  /** Runs an action, toasting its error. Returns undefined on failure. */
  async attempt<T>(fn: () => Promise<T>, ok?: string): Promise<T | undefined> {
    try {
      const r = await fn();
      if (ok) this.notify(ok);
      return r;
    } catch (e) {
      this.notify(errorText(e), 'error');
      return undefined;
    }
  }
}

export const app = new App();
