<script lang="ts">
  import { onMount } from 'svelte';
  import Icon from './components/Icon.svelte';
  import ProjectSwitcher from './components/ProjectSwitcher.svelte';
  import { app } from './lib/app.svelte';
  import { href, router } from './lib/router.svelte';
  import Board from './views/Board.svelte';
  import GitHub from './views/GitHub.svelte';
  import Schedules from './views/Schedules.svelte';
  import Settings from './views/Settings.svelte';
  import Skills from './views/Skills.svelte';
  import Ticket from './views/Ticket.svelte';

  const LAST = 'ra-project';
  let switching = $state(false);
  const route = $derived(router.route);
  let lastProject = $state<number | null>(readLast());

  function readLast(): number | null {
    try {
      const v = localStorage.getItem(LAST);
      return v ? +v : null;
    } catch {
      return null;
    }
  }

  const projectId = $derived('projectId' in route ? route.projectId : lastProject);
  const project = $derived(projectId !== null ? app.project(projectId) : undefined);
  const fallback = $derived(project ?? app.state?.projects[0]);

  $effect(() => {
    if ('projectId' in route) {
      lastProject = route.projectId;
      try {
        localStorage.setItem(LAST, String(route.projectId));
      } catch {
        // storage unavailable
      }
    }
  });

  $effect(() => {
    if (route.name === 'home' && app.state) {
      if (fallback) router.go({ name: 'board', projectId: fallback.id }, true);
      else switching = true;
    }
  });

  onMount(() => {
    app.load();
  });

  // New screen starts at the top; switching tabs inside a ticket keeps the scroll position.
  const screenKey = $derived(route.name === 'ticket' ? `ticket/${route.ticketId}` : JSON.stringify(route));
  $effect(() => {
    void screenKey;
    window.scrollTo(0, 0);
  });

  const nav = $derived(
    fallback
      ? [
          { name: 'board', label: 'Board', icon: 'board', link: href({ name: 'board', projectId: fallback.id }), on: route.name === 'board' || route.name === 'ticket' },
          { name: 'github', label: 'GitHub', icon: 'github', link: href({ name: 'github', projectId: fallback.id }), on: route.name === 'github' },
          { name: 'schedules', label: 'Jobs', icon: 'clock', link: href({ name: 'schedules', projectId: fallback.id }), on: route.name === 'schedules' },
          { name: 'skills', label: 'Skills', icon: 'skills', link: href({ name: 'skills' }), on: route.name === 'skills' },
          { name: 'settings', label: 'Settings', icon: 'settings', link: href({ name: 'settings' }), on: route.name === 'settings' },
        ]
      : [],
  ) as { name: string; label: string; icon: 'board' | 'github' | 'clock' | 'skills' | 'settings'; link: string; on: boolean }[];
</script>

<header class="topbar">
  <button class="switcher" type="button" onclick={() => (switching = true)} aria-haspopup="dialog">
    <span class="mark" aria-hidden="true"></span>
    <span class="pname">{fallback?.name ?? 'remote-agent'}</span>
    <Icon name="chevron" size={16} />
  </button>
  <nav class="topnav" aria-label="Main">
    {#each nav as n (n.name)}
      <a class={{ on: n.on }} href={n.link} aria-current={n.on ? 'page' : undefined}>{n.label}</a>
    {/each}
  </nav>
</header>

<main>
  {#if app.error && !app.state}
    <div class="page">
      <div class="empty">
        <p>Could not reach the server: {app.error}</p>
        <button class="btn" type="button" onclick={() => app.load()}>Try again</button>
      </div>
    </div>
  {:else if !app.state}
    <div class="page"><p class="muted">Loading…</p></div>
  {:else if route.name === 'settings'}
    <Settings />
  {:else if route.name === 'skills'}
    <Skills skill={route.skill} file={route.file} />
  {:else if 'projectId' in route && !project}
    <div class="page"><div class="empty"><p>Project not found.</p><button class="btn" type="button" onclick={() => (switching = true)}>Pick a project</button></div></div>
  {:else if project && route.name === 'board'}
    <Board {project} />
  {:else if project && route.name === 'ticket'}
    {#key route.ticketId}<Ticket {project} ticketId={route.ticketId} tab={route.tab} />{/key}
  {:else if project && route.name === 'github'}
    <GitHub {project} />
  {:else if project && route.name === 'schedules'}
    <Schedules {project} />
  {:else if !app.state.projects.length}
    <div class="page"><div class="empty"><p>No projects yet.</p><button class="btn primary" type="button" onclick={() => (switching = true)}>Add a project</button></div></div>
  {/if}
</main>

<nav class="bottomnav" aria-label="Main">
  {#each nav as n (n.name)}
    <a class={{ on: n.on }} href={n.link} aria-current={n.on ? 'page' : undefined}><Icon name={n.icon} size={22} /><span>{n.label}</span></a>
  {/each}
</nav>

{#if app.state}<ProjectSwitcher bind:open={switching} current={fallback} />{/if}

{#if app.toast}
  <div class="toast {app.toast.kind}" role={app.toast.kind === 'error' ? 'alert' : 'status'}>{app.toast.text}</div>
{/if}

<style>
  .topbar {
    position: sticky;
    top: 0;
    z-index: 10;
    height: var(--topbar-h);
    display: flex;
    align-items: center;
    gap: 16px;
    padding: 0 8px;
    background: var(--surface);
    border-bottom: 1px solid var(--line);
  }
  .switcher {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    height: 40px;
    padding: 0 10px;
    border: 0;
    border-radius: var(--r-m);
    background: transparent;
    font-weight: 600;
    font-size: 1rem;
    cursor: pointer;
    min-width: 0;
  }
  .pname {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .mark {
    width: 18px;
    height: 18px;
    border-radius: 5px;
    flex: none;
    background:
      linear-gradient(90deg, var(--lamp-needs_input) 0 33%, var(--lamp-working) 33% 66%, var(--lamp-in_review) 66%) bottom / 100% 5px no-repeat,
      var(--ink);
  }
  .topnav {
    display: none;
    gap: 4px;
    margin-left: auto;
  }
  .topnav a {
    padding: 8px 12px;
    border-radius: var(--r-m);
    color: var(--ink-2);
    text-decoration: none;
    font-weight: 500;
  }
  .topnav a.on {
    background: var(--sunken);
    color: var(--ink);
  }
  .bottomnav {
    position: fixed;
    inset: auto 0 0;
    z-index: 10;
    display: grid;
    grid-auto-flow: column;
    grid-auto-columns: 1fr;
    height: calc(var(--nav-h) + var(--safe-b));
    padding-bottom: var(--safe-b);
    background: var(--surface);
    border-top: 1px solid var(--line);
  }
  .bottomnav a {
    display: grid;
    place-items: center;
    align-content: center;
    gap: 2px;
    color: var(--muted);
    text-decoration: none;
    font-size: 0.7rem;
    font-weight: 500;
  }
  .bottomnav a.on {
    color: var(--ink);
  }
  .toast {
    position: fixed;
    left: 16px;
    right: 16px;
    bottom: calc(var(--nav-h) + var(--safe-b) + 80px);
    z-index: 50;
    max-width: 480px;
    margin: 0 auto;
    padding: 12px 16px;
    border-radius: var(--r-m);
    background: var(--ink);
    color: var(--bg);
    font-size: 0.9rem;
    box-shadow: var(--shadow-sheet);
  }
  .toast.error {
    background: var(--bad);
    color: #fff;
  }
  @media (min-width: 900px) {
    .topbar {
      padding: 0 16px;
    }
    .topnav {
      display: flex;
    }
    .bottomnav {
      display: none;
    }
    .toast {
      bottom: 24px;
    }
  }
</style>
