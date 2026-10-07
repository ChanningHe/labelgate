import { describe, expect, test } from 'vitest';
import type { ManagedResource } from '../api/client';
import { countByStatus, filterResources, sortResources } from './query';

const base: ManagedResource = {
  id: '',
  resource_type: 'dns',
  hostname: '',
  service_name: 'web',
  status: 'active',
  cleanup_enabled: false,
  created_at: '2026-10-01T00:00:00Z',
  updated_at: '2026-10-01T00:00:00Z',
};

const make = (patch: Partial<ManagedResource>): ManagedResource => ({ ...base, ...patch });

const rows = [
  make({ id: '1', hostname: 'b.example.com', content: '203.0.113.1', status: 'active', container_name: 'web' }),
  make({ id: '2', hostname: 'a.example.com', content: '203.0.113.2', status: 'error', container_name: 'api' }),
  make({ id: '3', hostname: 'C.example.com', content: 'example.com', status: 'orphaned', container_name: 'legacy' }),
];

describe('filterResources', () => {
  test('returns everything for status all and an empty query', () => {
    expect(filterResources(rows, { status: 'all', query: '', fields: ['hostname'] })).toHaveLength(3);
  });

  test('keeps only the selected status', () => {
    const out = filterResources(rows, { status: 'error', query: '', fields: ['hostname'] });

    expect(out.map((r) => r.id)).toEqual(['2']);
  });

  test('matches the query case-insensitively across the given fields', () => {
    const out = filterResources(rows, { status: 'all', query: ' LEGACY ', fields: ['hostname', 'container_name'] });

    expect(out.map((r) => r.id)).toEqual(['3']);
  });

  test('ignores fields that are not searchable', () => {
    const out = filterResources(rows, { status: 'all', query: 'legacy', fields: ['hostname'] });

    expect(out).toEqual([]);
  });
});

describe('sortResources', () => {
  test('sorts by a field ascending, ignoring case', () => {
    expect(sortResources(rows, 'hostname', 'asc').map((r) => r.id)).toEqual(['2', '1', '3']);
  });

  test('sorts descending', () => {
    expect(sortResources(rows, 'hostname', 'desc').map((r) => r.id)).toEqual(['3', '1', '2']);
  });

  test('puts missing values first when ascending', () => {
    const withMissing = [...rows, make({ id: '4', hostname: 'd.example.com' })];

    expect(sortResources(withMissing, 'container_name', 'asc')[0].id).toBe('4');
  });

  test('does not mutate the input', () => {
    const copy = [...rows];
    sortResources(rows, 'hostname', 'asc');

    expect(rows).toEqual(copy);
  });
});

describe('countByStatus', () => {
  test('counts each status and the total', () => {
    expect(countByStatus(rows)).toEqual({ all: 3, active: 1, orphaned: 1, error: 1 });
  });

  test('returns zeros for an empty list', () => {
    expect(countByStatus([])).toEqual({ all: 0, active: 0, orphaned: 0, error: 0 });
  });
});
