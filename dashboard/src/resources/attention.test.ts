import { describe, expect, test } from 'vitest';
import type { ManagedResource } from '../api/client';
import { collectAttention, describeProblem } from './attention';

const make = (patch: Partial<ManagedResource>): ManagedResource => ({
  id: 'x',
  resource_type: 'dns',
  hostname: 'a.example.com',
  service_name: 'web',
  status: 'active',
  cleanup_enabled: false,
  created_at: '2026-10-01T00:00:00Z',
  updated_at: '2026-10-01T00:00:00Z',
  ...patch,
});

describe('describeProblem', () => {
  test('returns nothing for an active resource', () => {
    expect(describeProblem(make({}), 'record')).toBeNull();
  });

  test('returns the server error for an errored resource', () => {
    expect(describeProblem(make({ status: 'error', last_error: 'boom' }), 'record')).toBe('boom');
  });

  test('explains an error without a message', () => {
    expect(describeProblem(make({ status: 'error' }), 'record')).toBe(
      'Cloudflare rejected this record. Check the labelgate logs for details.',
    );
  });

  test('says an orphan with cleanup on will be deleted', () => {
    const r = make({ status: 'orphaned', cleanup_enabled: true, container_name: 'web' });

    expect(describeProblem(r, 'record')).toBe(
      'Container web is gone. This record will be deleted after sync.remove_delay.',
    );
  });

  test('says an orphan with cleanup off is kept', () => {
    const r = make({ status: 'orphaned', cleanup_enabled: false });

    expect(describeProblem(r, 'access app')).toBe(
      'The container is gone. The access app stays on Cloudflare because cleanup is off.',
    );
  });
});

describe('collectAttention', () => {
  test('lists errors before orphans, newest first within each, and skips healthy resources', () => {
    const out = collectAttention({
      dns: [
        make({ id: 'ok', status: 'active' }),
        make({ id: 'old-orphan', status: 'orphaned', updated_at: '2026-10-01T00:00:00Z' }),
      ],
      tunnel: [make({ id: 'new-orphan', status: 'orphaned', updated_at: '2026-10-05T00:00:00Z' })],
      access: [make({ id: 'err', status: 'error', updated_at: '2026-09-01T00:00:00Z' })],
    });

    expect(out.map((x) => [x.kind, x.resource.id])).toEqual([
      ['access', 'err'],
      ['tunnel', 'new-orphan'],
      ['dns', 'old-orphan'],
    ]);
  });
});
