<script lang="ts">
  const paths = {
    board: 'M4 5h4v14H4zM10 5h4v9h-4zM16 5h4v6h-4z',
    github: 'M12 3a9 9 0 0 0-2.8 17.5c.4.1.6-.2.6-.4v-1.6c-2.5.5-3-1.1-3-1.1-.4-1-1-1.3-1-1.3-.8-.6.1-.6.1-.6.9.1 1.4.9 1.4.9.8 1.4 2.1 1 2.6.8.1-.6.3-1 .6-1.2-2-.2-4.1-1-4.1-4.4 0-1 .4-1.8.9-2.4-.1-.2-.4-1.1.1-2.4 0 0 .8-.2 2.5.9a8.5 8.5 0 0 1 4.5 0c1.7-1.1 2.5-.9 2.5-.9.5 1.3.2 2.2.1 2.4.6.6.9 1.4.9 2.4 0 3.4-2.1 4.2-4.1 4.4.3.3.6.8.6 1.6v2.4c0 .2.2.5.6.4A9 9 0 0 0 12 3z',
    clock: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zm0 4v5l3.5 2',
    settings: 'M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6zM19.4 13a7.6 7.6 0 0 0 0-2l2-1.6-2-3.4-2.4 1a7.4 7.4 0 0 0-1.7-1L15 3.5h-4L10.6 6a7.4 7.4 0 0 0-1.7 1l-2.4-1-2 3.4 2 1.6a7.6 7.6 0 0 0 0 2l-2 1.6 2 3.4 2.4-1c.5.4 1.1.7 1.7 1l.4 2.5h4l.4-2.5c.6-.3 1.2-.6 1.7-1l2.4 1 2-3.4z',
    skills: 'M5 4h10l4 4v12H5zM14 4v5h5M8 13h8M8 17h5',
    plus: 'M12 5v14M5 12h14',
    chevron: 'M6 9l6 6 6-6',
    back: 'M15 5l-7 7 7 7',
    close: 'M6 6l12 12M18 6L6 18',
    play: 'M8 5l11 7-11 7z',
    pause: 'M8 5v14M16 5v14',
    stop: 'M6 6h12v12H6z',
    retry: 'M4 12a8 8 0 0 1 14-5.3M20 4v4h-4M20 12a8 8 0 0 1-14 5.3M4 20v-4h4',
    calendar: 'M4 6h16v14H4zM4 10h16M8 3v4M16 3v4',
    check: 'M5 12.5l4.5 4.5L19 7.5',
    x: 'M7 7l10 10M17 7L7 17',
    dot: 'M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6z',
    send: 'M4 12l16-8-6 16-2.5-6.5z',
    link: 'M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1',
    up: 'M12 19V5M6 11l6-6 6 6',
    down: 'M12 5v14M6 13l6 6 6-6',
    trash: 'M5 7h14M10 7V4h4v3M7 7l1 13h8l1-13',
    merge: 'M7 4v16M7 8a5 5 0 0 0 5 5h5M17 13a2 2 0 1 0 0 .1',
    pr: 'M7 4v16M17 20V9a3 3 0 0 0-3-3h-4M12 3l-2 3 2 3',
    bell: 'M6 16V11a6 6 0 0 1 12 0v5l2 2H4zM10 21h4',
  } as const;

  let { name, size = 20 }: { name: keyof typeof paths; size?: number } = $props();
  const filled = $derived(name === 'play' || name === 'dot' || name === 'github');
</script>

<svg
  width={size}
  height={size}
  viewBox="0 0 24 24"
  fill={filled ? 'currentColor' : 'none'}
  stroke={filled ? 'none' : 'currentColor'}
  stroke-width="1.8"
  stroke-linecap="round"
  stroke-linejoin="round"
  aria-hidden="true"><path d={paths[name]} /></svg>
