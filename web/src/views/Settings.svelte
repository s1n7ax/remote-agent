<script lang="ts">
  import { onMount } from 'svelte';
  import Chips from '../components/Chips.svelte';
  import Icon from '../components/Icon.svelte';
  import { api } from '../lib/api';
  import { app } from '../lib/app.svelte';
  import { clock, errorText, relative, usd } from '../lib/format';
  import { enablePush, pushEnabled, pushSupported } from '../lib/push';
  import { applyTheme, getTheme, type Theme } from '../lib/theme';
  import { NOTIFY_LABEL, type Account, type NotificationPrefs, type NotifyEvent, type Project } from '../lib/types';

  const s = $derived(app.state!);
  // Writable deriveds: follow the server state, overridable by local edits.
  let accounts = $derived([...s.accounts].sort((a, b) => a.priority - b.priority));
  let label = $state('');
  let token = $state('');
  let dailyCap = $derived(s.settings.dailyCapUsd);
  let ticketCap = $derived(s.settings.ticketCapUsd);
  let testing = $state(false);
  let prefs = $state<NotificationPrefs | null>(null);
  let device = $state<'unknown' | 'on' | 'off' | 'unsupported'>('unknown');
  let deviceBusy = $state(false);
  let theme = $state<Theme>(getTheme());
  // Per-project run time limits in minutes; 0 uses the server default.
  let timeouts = $derived(Object.fromEntries(s.projects.map((p) => [p.id, p.runTimeoutMinutes])) as Record<number, number>);

  onMount(() => {
    api
      .notifications()
      .then((p) => (prefs = p))
      .catch((e) => app.notify(errorText(e), 'error'));
    if (!pushSupported()) device = 'unsupported';
    else pushEnabled().then((on) => (device = on ? 'on' : 'off'));
  });

  async function addAccount(e: SubmitEvent) {
    e.preventDefault();
    const last = accounts.at(-1);
    const a = await app.attempt(() => api.addAccount(label.trim(), token.trim(), last ? last.priority + 1 : 0), 'Account added');
    if (!a) return;
    label = '';
    token = '';
    await app.load();
  }

  async function removeAccount(a: Account) {
    if (!confirm(`Remove “${a.label}”? Its token is deleted from the server.`)) return;
    const ok = await app.attempt(() => api.deleteAccount(a.id), 'Account removed');
    if (ok) await app.load();
  }

  async function move(i: number, d: -1 | 1) {
    const next = [...accounts];
    [next[i], next[i + d]] = [next[i + d], next[i]];
    accounts = next;
    // Priorities are dense 0..n-1 in list order; only changed ones are sent.
    const res = await app.attempt(() =>
      Promise.all(next.flatMap((a, p) => (a.priority === p ? [] : [api.patchAccount(a.id, { priority: p })]))),
    );
    await app.load();
    if (!res) accounts = [...s.accounts];
  }

  async function clearLimit(a: Account) {
    const res = await app.attempt(() => api.patchAccount(a.id, { clearLimited: true }), `${a.label} marked available`);
    if (res) await app.load();
  }

  async function sendTest() {
    testing = true;
    await app.attempt(() => api.pushTest(), 'Test notification sent');
    testing = false;
  }

  async function saveLimits(e: SubmitEvent) {
    e.preventDefault();
    const res = await app.attempt(
      () => api.updateSettings({ dailyCapUsd: dailyCap, ticketCapUsd: ticketCap }),
      'Limits saved',
    );
    if (res && app.state) app.state.settings = res;
  }

  async function saveTimeout(e: SubmitEvent, p: Project) {
    e.preventDefault();
    const minutes = Math.max(0, Math.round(timeouts[p.id] || 0));
    const res = await app.attempt(() => api.updateProject(p.id, { runTimeoutMinutes: minutes }), `Time limit saved for ${p.name}`);
    if (res) await app.load();
  }

  const isOn = (ev: NotifyEvent) => prefs?.prefs?.[ev] ?? s.notifyEvents?.[ev] ?? false;

  async function toggle(ev: NotifyEvent) {
    if (!prefs) return;
    const next = { ...(prefs.prefs ?? {}), [ev]: !isOn(ev) };
    prefs.prefs = next;
    const res = await app.attempt(() => api.setNotifications(next));
    if (res) prefs = res;
  }

  async function enableDevice() {
    deviceBusy = true;
    try {
      await enablePush(prefs?.vapidPublicKey || s.vapidPublicKey);
      device = 'on';
      app.notify('Notifications enabled on this device');
    } catch (e) {
      app.notify(errorText(e), 'error');
    } finally {
      deviceBusy = false;
    }
  }

  function usage(a: Account) {
    if (a.exhaustedTill && new Date(a.exhaustedTill).getTime() > Date.now()) return `At limit until ${clock(a.exhaustedTill)} (${relative(a.exhaustedTill)})`;
    return `${Math.round(a.utilization * 100)}% used${a.warning ? ' — nearing limit' : ''}`;
  }
</script>

<div class="page">
  <h1>Settings</h1>

  <section class="panel">
    <h2>Claude accounts</h2>
    <p class="muted small">Runs use the first account with quota. When one hits its limit the run moves to the next.</p>
    <ol class="accounts">
      {#each accounts as a, i (a.id)}
        {@const exhausted = !!a.exhaustedTill && new Date(a.exhaustedTill).getTime() > Date.now()}
        <li>
          <span class="prio">{i + 1}</span>
          <div class="grow">
            <strong>{a.label}</strong>
            <p class={['small', { bad: exhausted, warn: !exhausted && a.warning }]}>
              {usage(a)}
              {#if exhausted || a.warning}<button class="link" type="button" onclick={() => clearLimit(a)}>Mark available</button>{/if}
            </p>
            <span class="meter"><span class={{ bad: exhausted, warn: a.warning }} style:width="{Math.min(100, a.utilization * 100)}%"></span></span>
          </div>
          <div class="ops">
            <button class="icon-btn" type="button" aria-label="Move {a.label} up" disabled={i === 0} onclick={() => move(i, -1)}><Icon name="up" size={18} /></button>
            <button class="icon-btn" type="button" aria-label="Move {a.label} down" disabled={i === accounts.length - 1} onclick={() => move(i, 1)}><Icon name="down" size={18} /></button>
            <button class="icon-btn" type="button" aria-label="Remove {a.label}" onclick={() => removeAccount(a)}><Icon name="trash" size={18} /></button>
          </div>
        </li>
      {:else}
        <li class="muted">No accounts yet. Claude tickets wait until one is added.</li>
      {/each}
    </ol>
    <form class="add" onsubmit={addAccount}>
      <label class="field">Label <input type="text" bind:value={label} placeholder="Max — personal" /></label>
      <label class="field">
        Token
        <input type="password" bind:value={token} autocomplete="off" placeholder="sk-ant-oat01-…" />
        <span class="muted small">Run <code>claude setup-token</code> on any machine and paste the token.</span>
      </label>
      <button class="btn" type="submit" disabled={!label.trim() || !token.trim()}>Add account</button>
    </form>
  </section>

  <section class="panel">
    <h2>OpenRouter (Pi)</h2>
    <p class="small row wrap">
      {#if s.settings.openrouterConfigured}<span class="badge ok">Key configured</span>{:else}<span class="badge bad">No key</span>{/if}
      <span class="muted">Spent today: {usd(s.settings.spentTodayUsd)}{#if s.settings.dailyCapUsd > 0} of {usd(s.settings.dailyCapUsd)}{/if}</span>
    </p>
    <p class="muted small">The key is read from the file set in the server configuration (<code>openrouterKeyFile</code>).</p>
  </section>

  <form class="panel" onsubmit={saveLimits}>
    <h2>Limits</h2>
    <p class="small">Parallel runs: <strong>{s.settings.maxParallelRuns}</strong> <span class="muted">(set in the server configuration)</span></p>
    <div class="grid3">
      <label class="field">Daily spend cap ($) <input type="number" min="0" step="0.5" bind:value={dailyCap} /></label>
      <label class="field">Per-ticket cap ($) <input type="number" min="0" step="0.5" bind:value={ticketCap} /></label>
    </div>
    <p class="muted small">Spend caps apply to Pi; reaching one pauses the run and asks on the card. 0 means no cap. The per-ticket cap is the default for new tickets.</p>
    <button class="btn" type="submit">Save limits</button>
  </form>

  <section class="panel">
    <h2>Run time limit</h2>
    <p class="muted small">A run that takes longer is stopped, and its ticket fails with the reason; Retry continues it. 0 uses the default of {s.settings.runTimeoutMinutes} minutes, set in the server configuration.</p>
    {#each s.projects as p (p.id)}
      <form class="timeout" onsubmit={(e) => saveTimeout(e, p)}>
        <label class="field">{p.name} (minutes) <input type="number" min="0" step="5" bind:value={timeouts[p.id]} /></label>
        <button class="btn" type="submit">Save</button>
      </form>
    {:else}
      <p class="muted small">No projects yet.</p>
    {/each}
  </section>

  <section class="panel">
    <h2>Notifications</h2>
    <div class="row wrap">
      {#if device === 'on'}
        <span class="badge ok">On for this device</span>
      {:else if device === 'unsupported'}
        <span class="muted small">This browser has no Web Push. On iOS, add the app to your home screen first.</span>
      {:else}
        <button class="btn primary" type="button" disabled={deviceBusy} onclick={enableDevice}><Icon name="bell" size={18} /> Enable on this device</button>
      {/if}
      {#if device === 'on'}<button class="btn small" type="button" disabled={testing} onclick={sendTest}>Send a test</button>{/if}
    </div>
    {#if prefs}
      <ul class="toggles">
        {#each Object.keys(NOTIFY_LABEL) as NotifyEvent[] as ev (ev)}
          <li>
            <label>
              <span class="grow">{NOTIFY_LABEL[ev]}</span>
              <input type="checkbox" checked={isOn(ev)} onchange={() => toggle(ev)} />
            </label>
          </li>
        {/each}
      </ul>
    {/if}
  </section>

  <section class="panel">
    <h2>Appearance</h2>
    <Chips
      label="Theme"
      options={[
        { value: 'system', label: 'System' },
        { value: 'light', label: 'Light' },
        { value: 'dark', label: 'Dark' },
      ]}
      value={theme}
      onchange={(t) => {
        theme = t;
        applyTheme(t);
      }} />
  </section>
</div>

<style>
  .accounts {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 2px;
  }
  .accounts li {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 0;
    border-bottom: 1px solid var(--line);
  }
  .prio {
    width: 24px;
    height: 24px;
    border-radius: 50%;
    background: var(--sunken);
    display: grid;
    place-items: center;
    font-size: 0.78rem;
    font-weight: 600;
    flex: none;
  }
  .ops {
    display: flex;
  }
  .ops .icon-btn {
    width: 36px;
  }
  .ops .icon-btn:disabled {
    opacity: 0.3;
  }
  .meter {
    display: block;
    height: 4px;
    border-radius: 2px;
    background: var(--sunken);
    margin-top: 4px;
    overflow: hidden;
  }
  .meter span {
    display: block;
    height: 100%;
    background: var(--ok);
  }
  .meter span.warn {
    background: var(--attention);
  }
  .meter span.bad {
    background: var(--bad);
  }
  p.bad {
    color: var(--bad);
  }
  p.warn {
    color: var(--attention-ink);
  }
  .add {
    display: grid;
    gap: 10px;
    padding-top: 6px;
  }
  .add .btn {
    justify-self: start;
  }
  form.panel > .btn {
    justify-self: start;
  }
  .link {
    border: 0;
    background: none;
    padding: 0 0 0 6px;
    color: var(--primary);
    font-size: inherit;
    cursor: pointer;
    text-decoration: underline;
  }
  .grid3 {
    display: grid;
    gap: 10px;
  }
  .timeout {
    display: flex;
    align-items: end;
    gap: 10px;
    margin-bottom: 10px;
  }
  .timeout .field {
    flex: 1;
    min-width: 0;
  }
  @media (min-width: 600px) {
    .link {
    border: 0;
    background: none;
    padding: 0 0 0 6px;
    color: var(--primary);
    font-size: inherit;
    cursor: pointer;
    text-decoration: underline;
  }
  .grid3 {
      grid-template-columns: repeat(2, 1fr);
    }
  }
  .toggles {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .toggles label {
    display: flex;
    align-items: center;
    gap: 10px;
    min-height: 48px;
    border-bottom: 1px solid var(--line);
    cursor: pointer;
  }
  .toggles li:last-child label {
    border-bottom: 0;
  }
  .toggles input {
    width: 22px;
    height: 22px;
    accent-color: var(--primary);
  }
</style>
