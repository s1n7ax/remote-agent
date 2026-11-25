// Tiny hash router: #/p/1/board, #/p/1/t/101/questions, #/settings …

export type Route =
  | { name: 'board'; projectId: number }
  | { name: 'ticket'; projectId: number; ticketId: number; tab: string }
  | { name: 'github'; projectId: number }
  | { name: 'schedules'; projectId: number }
  | { name: 'settings' }
  | { name: 'skills'; skill?: string; file?: string }
  | { name: 'home' };

/** Parses a location hash into a route. */
export function parse(hash: string): Route {
  const parts = hash.replace(/^#\/?/, '').split('/').filter(Boolean).map(decodeURIComponent);
  const [a, b, c, d, e] = parts;
  if (a === 'p' && b && /^\d+$/.test(b)) {
    const projectId = +b;
    if (c === 't' && d && /^\d+$/.test(d)) return { name: 'ticket', projectId, ticketId: +d, tab: e ?? 'questions' };
    if (c === 'github') return { name: 'github', projectId };
    if (c === 'schedules') return { name: 'schedules', projectId };
    return { name: 'board', projectId };
  }
  if (a === 'settings') return { name: 'settings' };
  if (a === 'skills') return { name: 'skills', skill: b, file: parts.slice(2).join('/') || undefined };
  return { name: 'home' };
}

/** Builds a hash link for a route. */
export function href(r: Route): string {
  switch (r.name) {
    case 'board':
      return `#/p/${r.projectId}/board`;
    case 'ticket':
      return `#/p/${r.projectId}/t/${r.ticketId}/${r.tab}`;
    case 'github':
      return `#/p/${r.projectId}/github`;
    case 'schedules':
      return `#/p/${r.projectId}/schedules`;
    case 'settings':
      return '#/settings';
    case 'skills':
      return r.skill ? `#/skills/${encodeURIComponent(r.skill)}${r.file ? '/' + r.file.split('/').map(encodeURIComponent).join('/') : ''}` : '#/skills';
    case 'home':
      return '#/';
  }
}

class Router {
  route = $state<Route>(parse(typeof location === 'undefined' ? '' : location.hash));

  constructor() {
    if (typeof window !== 'undefined') {
      window.addEventListener('hashchange', () => {
        this.route = parse(location.hash);
      });
    }
  }

  go(r: Route, replace = false): void {
    const h = href(r);
    if (replace) {
      history.replaceState(null, '', h);
      this.route = parse(h);
    } else location.hash = h;
  }
}

export const router = new Router();
