import type { ManagedResource } from '../api/client';

export type StatusFilter = 'all' | 'active' | 'orphaned' | 'error';
export type SortDirection = 'asc' | 'desc';
export type ResourceField = keyof ManagedResource;

export const STATUS_FILTERS: readonly { value: StatusFilter; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'active', label: 'Active' },
  { value: 'orphaned', label: 'Orphaned' },
  { value: 'error', label: 'Error' },
];

interface FilterOptions {
  status: StatusFilter;
  query: string;
  fields: readonly ResourceField[];
}

const LOCAL_AGENT = 'local';

/** Resources from the server's own Docker host have no agent_id. */
export const agentLabel = (r: ManagedResource) => r.agent_id || LOCAL_AGENT;

const asText = (value: unknown) => (value == null ? '' : String(value).toLowerCase());

export function filterResources(rows: readonly ManagedResource[], { status, query, fields }: FilterOptions) {
  const q = query.trim().toLowerCase();
  return rows.filter(
    (r) => (status === 'all' || r.status === status) && (!q || fields.some((f) => asText(r[f]).includes(q))),
  );
}

export function sortResources(rows: readonly ManagedResource[], field: ResourceField, direction: SortDirection) {
  const sign = direction === 'asc' ? 1 : -1;
  return [...rows].sort((a, b) => asText(a[field]).localeCompare(asText(b[field])) * sign);
}

export function countByStatus(rows: readonly ManagedResource[]): Record<StatusFilter, number> {
  return rows.reduce(
    (acc, r) => (r.status in acc ? { ...acc, [r.status]: acc[r.status as StatusFilter] + 1 } : acc),
    { all: rows.length, active: 0, orphaned: 0, error: 0 },
  );
}
