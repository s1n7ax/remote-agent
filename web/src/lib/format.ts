const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });

/** "in 2 h", "5 min ago". */
export function relative(iso: string | undefined, nowMs = Date.now()): string {
  if (!iso) return '';
  const diff = (new Date(iso).getTime() - nowMs) / 1000;
  const abs = Math.abs(diff);
  if (abs < 45) return diff >= 0 ? 'in a moment' : 'just now';
  if (abs < 3600) return rtf.format(Math.round(diff / 60), 'minute');
  if (abs < 86400) return rtf.format(Math.round(diff / 3600), 'hour');
  return rtf.format(Math.round(diff / 86400), 'day');
}

/** Short local time, with the date when not today. */
export function clock(iso: string | undefined): string {
  if (!iso) return '';
  const d = new Date(iso);
  const today = new Date().toDateString() === d.toDateString();
  return d.toLocaleString(undefined, today ? { hour: '2-digit', minute: '2-digit' } : { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

/** Value for <input type="datetime-local"> from an ISO string. */
export function toLocalInput(iso: string | undefined): string {
  const d = iso ? new Date(iso) : new Date(Date.now() + 3600_000);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function usd(n: number): string {
  return `$${n.toFixed(2)}`;
}

export function errorText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
