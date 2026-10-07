import { describe, expect, test } from 'vitest';
import type { ManagedResource } from '../api/client';
import type { LabelLine } from '../labels/format';
import { equivalentLabels } from './equivalentLabels';
import { buildResourceIndex } from './useResourceIndex';

const make = (patch: Partial<ManagedResource>): ManagedResource => ({
  id: 'x',
  resource_type: 'dns',
  hostname: 'app.example.com',
  service_name: 'web',
  status: 'active',
  cleanup_enabled: false,
  created_at: '2026-10-01T00:00:00Z',
  updated_at: '2026-10-01T00:00:00Z',
  ...patch,
});

const empty = buildResourceIndex({});
const pairs = (lines: LabelLine[]) => lines.flatMap((l) => ('key' in l ? [[l.key, l.value]] : []));
const comments = (lines: LabelLine[]) => lines.flatMap((l) => ('comment' in l ? [l.comment] : []));

describe('equivalentLabels for DNS', () => {
  test('rebuilds a proxied A record with defaults left out', () => {
    const r = make({ record_type: 'A', content: '203.0.113.24', proxied: true });

    expect(pairs(equivalentLabels('dns', r, empty))).toEqual([
      ['dns.web.hostname', 'app.example.com'],
      ['dns.web.target', '203.0.113.24'],
    ]);
  });

  test('notes that A record targets are resolved addresses', () => {
    const r = make({ record_type: 'A', content: '203.0.113.24', proxied: true });

    expect(comments(equivalentLabels('dns', r, empty))[0]).toContain('target: auto');
  });

  test('includes type, proxied, ttl, credential and cleanup when they differ from defaults', () => {
    const r = make({
      record_type: 'CNAME',
      content: 'example.com',
      proxied: false,
      ttl: 300,
      credential: 'company',
      cleanup_enabled: true,
    });

    expect(pairs(equivalentLabels('dns', r, empty))).toEqual([
      ['dns.web.hostname', 'app.example.com'],
      ['dns.web.type', 'CNAME'],
      ['dns.web.target', 'example.com'],
      ['dns.web.proxied', 'false'],
      ['dns.web.ttl', '300'],
      ['dns.web.credential', 'company'],
      ['dns.web.cleanup', 'true'],
    ]);
  });

  test('omits automatic TTL and the default credential', () => {
    const r = make({ record_type: 'A', content: '203.0.113.24', proxied: false, ttl: 1, credential: 'default' });

    const keys = pairs(equivalentLabels('dns', r, empty)).map(([k]) => k);
    expect(keys).not.toContain('dns.web.ttl');
    expect(keys).not.toContain('dns.web.credential');
  });

  test('does not emit proxied for record types Cloudflare cannot proxy', () => {
    const r = make({ record_type: 'MX', content: 'mail.provider.com', proxied: false });

    expect(pairs(equivalentLabels('dns', r, empty)).map(([k]) => k)).not.toContain('dns.web.proxied');
  });

  test('flags MX fields that are not stored', () => {
    const r = make({ record_type: 'MX', content: 'mail.provider.com' });

    expect(comments(equivalentLabels('dns', r, empty)).join(' ')).toContain('priority');
  });

  test('attaches the Access policy protecting the same hostname', () => {
    const index = buildResourceIndex({
      access: [make({ id: 'a', resource_type: 'access_app', access_policy_name: 'team' })],
    });
    const r = make({ record_type: 'A', content: '203.0.113.24', proxied: true });

    expect(pairs(equivalentLabels('dns', r, index))).toContainEqual(['dns.web.access', 'team']);
  });
});

describe('equivalentLabels for tunnels', () => {
  test('rebuilds hostname, service, path, access and cleanup', () => {
    const index = buildResourceIndex({
      access: [make({ id: 'a', resource_type: 'access_app', access_policy_name: 'internal' })],
    });
    const r = make({
      resource_type: 'tunnel_ingress',
      service: 'http://webapp:80',
      path: '\\.(jpg|png)$',
      cleanup_enabled: true,
    });

    expect(pairs(equivalentLabels('tunnel', r, index))).toEqual([
      ['tunnel.web.hostname', 'app.example.com'],
      ['tunnel.web.service', 'http://webapp:80'],
      ['tunnel.web.path', '\\.(jpg|png)$'],
      ['tunnel.web.access', 'internal'],
      ['tunnel.web.cleanup', 'true'],
    ]);
  });
});

describe('equivalentLabels for Access', () => {
  test('rebuilds the policy template and attaches it to the tunnel service', () => {
    const index = buildResourceIndex({ tunnel: [make({ id: 't', resource_type: 'tunnel_ingress' })] });
    const r = make({
      resource_type: 'access_app',
      access_policy_name: 'team',
      access_decision: 'allow',
      access_app_name: 'Admin console',
    });

    const lines = equivalentLabels('access', r, index);
    expect(pairs(lines)).toEqual([
      ['access.team.policy.decision', 'allow'],
      ['access.team.app_name', 'Admin console'],
      ['tunnel.web.access', 'team'],
    ]);
    expect(comments(lines).join(' ')).toContain('include');
  });

  test('omits the default application name and attaches to DNS when no tunnel owns the hostname', () => {
    const index = buildResourceIndex({ dns: [make({ id: 'd' })] });
    const r = make({
      resource_type: 'access_app',
      access_policy_name: 'team',
      access_decision: 'bypass',
      access_app_name: 'labelgate-team',
    });

    expect(pairs(equivalentLabels('access', r, index))).toEqual([
      ['access.team.policy.decision', 'bypass'],
      ['dns.web.access', 'team'],
    ]);
  });
});
