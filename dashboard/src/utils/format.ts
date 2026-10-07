const SECOND = 1000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
const JUST_NOW_MS = 5 * SECOND;
const RELATIVE_LIMIT_MS = 7 * DAY;
// Go marshals an unset time.Time as 0001-01-01; treat anything before 1971 as unset.
const MIN_VALID_MS = Date.UTC(1971, 0, 1);

/**
 * Format an ISO timestamp as "12m ago", or as a calendar date when older than a week.
 * Missing, unparsable and Go zero times render as an em dash.
 */
export function formatRelative(iso: string | null | undefined, now: number = Date.now()): string {
  if (!iso) return '—';
  const time = Date.parse(iso);
  if (Number.isNaN(time) || time < MIN_VALID_MS) return '—';

  const diff = Math.max(0, now - time);
  if (diff < JUST_NOW_MS) return 'just now';
  if (diff < MINUTE) return `${Math.floor(diff / SECOND)}s ago`;
  if (diff < HOUR) return `${Math.floor(diff / MINUTE)}m ago`;
  if (diff < DAY) return `${Math.floor(diff / HOUR)}h ago`;
  if (diff < RELATIVE_LIMIT_MS) return `${Math.floor(diff / DAY)}d ago`;

  const date = new Date(time);
  return date.toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    year: date.getFullYear() !== new Date(now).getFullYear() ? 'numeric' : undefined,
  });
}

const IPV4 = /^(\d{1,3})\.(\d{1,3})\.\d{1,3}\.\d{1,3}$/;

/**
 * Hide the host part of a public IP so it is not exposed on a shared screen.
 * The real value is only rendered when the user reveals it.
 */
export function maskIP(ip: string | undefined): string {
  if (!ip) return '—';
  const v4 = IPV4.exec(ip);
  if (v4) return `${v4[1]}.${v4[2]}.•••.••`;
  if (ip.includes(':')) return `${ip.split(':').slice(0, 2).join(':')}:••••`;
  return '•••';
}

/**
 * Release builds report the git tag (v0.1.8); local builds may report 0.1.8 or "dev".
 */
export function formatVersion(version: string): string {
  if (!version) return '—';
  return /^\d/.test(version) ? `v${version}` : version;
}

/**
 * Truncate a container ID to first 12 characters.
 */
export function shortId(id: string): string {
  if (!id) return '—';
  return id.length > 12 ? id.slice(0, 12) : id;
}
