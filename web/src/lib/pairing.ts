// A pairing link (#/login?token=…) carries the login token. It is taken out
// of the address bar and history as soon as the page sees it, signed in or
// not, and handed to the sign-in screen if that wants it.

let pending: string | null = null;
const listeners = new Set<(token: string) => void>();

function capture(): void {
  const m = /^#\/login\?(.*)$/.exec(location.hash);
  if (!m) return;
  const token = new URLSearchParams(m[1]).get('token');
  history.replaceState(null, '', '#/');
  if (!token) return;
  if (listeners.size) listeners.forEach((l) => l(token));
  else pending = token;
}

/** Starts watching for pairing links; call once before the app mounts. */
export function initPairing(): void {
  capture();
  window.addEventListener('hashchange', capture);
}

/** Delivers a pairing token (one already seen, or the next); returns an unsubscribe function. */
export function onPairing(f: (token: string) => void): () => void {
  if (pending) {
    const t = pending;
    pending = null;
    f(t);
  }
  listeners.add(f);
  return () => listeners.delete(f);
}

/** Forgets a token nobody asked for (the browser is already signed in). */
export function dropPairing(): void {
  pending = null;
}
