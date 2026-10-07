import type { ReactNode } from 'react';
import type { ManagedResource } from '../api/client';
import { Chip, DecisionPill, StatusPill } from '../components/Badges';
import { formatRelative, shortId } from '../utils/format';
import { KIND_META, type KindId, type KindMeta } from './meta';
import { agentLabel, type ResourceField } from './query';
import type { ResourceIndex } from './useResourceIndex';
import { AccessChip, HostnameCell, Mono, OpenAction, ProxyStatus, ServiceURL, SourceCell } from './cells';

// One descriptor per resource kind. The shared ResourcePage, table and detail
// panel render from these; adding a column or detail row happens here only.

export interface Column {
  id: string;
  label: string;
  render: (r: ManagedResource, index: ResourceIndex) => ReactNode;
  // Field to sort by when the header is clicked; omitted columns are not sortable.
  sortKey?: ResourceField;
  // Secondary columns hide on narrow screens.
  isSecondary?: boolean;
}

export interface DetailSection {
  title: string;
  rows: [label: string, value: ReactNode][];
}

export interface ResourceKind extends KindMeta {
  description: string;
  searchFields: readonly ResourceField[];
  searchPlaceholder: string;
  columns: readonly Column[];
  details: (r: ManagedResource, index: ResourceIndex) => DetailSection[];
}

const AUTO_TTL = 1; // Cloudflare reports automatic TTL as 1.

const statusColumn: Column = {
  id: 'status',
  label: 'Status',
  sortKey: 'status',
  render: (r) => <StatusPill status={r.status} />,
};

const sourceColumn: Column = {
  id: 'source',
  label: 'Source',
  sortKey: 'container_name',
  render: (r) => <SourceCell resource={r} />,
  isSecondary: true,
};

const ttlLabel = (r: ManagedResource) => (r.proxied || !r.ttl || r.ttl <= AUTO_TTL ? 'Auto' : `${r.ttl}s`);

const policyFor = (r: ManagedResource, index: ResourceIndex) =>
  index.byHostname.access.get(r.hostname)?.access_policy_name;

function sourceSection(r: ManagedResource): DetailSection {
  const rows: DetailSection['rows'] = [
    ['Service name', <Mono>{r.service_name}</Mono>],
    [
      'Container',
      r.container_name ? (
        <span title={r.container_id}>
          {r.container_name} <Mono isDim>{shortId(r.container_id ?? '')}</Mono>
        </span>
      ) : (
        '—'
      ),
    ],
    ['Agent', <Mono>{agentLabel(r)}</Mono>],
  ];
  return { title: 'Source', rows: r.credential ? [...rows, ['Credential', <Mono>{r.credential}</Mono>]] : rows };
}

function lifecycleSection(r: ManagedResource): DetailSection {
  return {
    title: 'Lifecycle',
    rows: [
      ['Created', formatRelative(r.created_at)],
      ['Updated', formatRelative(r.updated_at)],
      ['Cleanup', r.cleanup_enabled ? 'On, deleted after the container is gone' : 'Off, kept on Cloudflare'],
    ],
  };
}

const withCommonSections = (section: DetailSection, r: ManagedResource) => [
  section,
  sourceSection(r),
  lifecycleSection(r),
];

const dns: ResourceKind = {
  ...KIND_META.dns,
  description: 'Records labelgate created from dns labels on your containers.',
  searchFields: ['hostname', 'content', 'container_name'],
  searchPlaceholder: 'Search hostname, content, container',
  columns: [
    statusColumn,
    { id: 'hostname', label: 'Hostname', sortKey: 'hostname', render: (r) => <HostnameCell hostname={r.hostname} /> },
    { id: 'type', label: 'Type', sortKey: 'record_type', render: (r) => <Chip>{r.record_type ?? '—'}</Chip> },
    {
      id: 'content',
      label: 'Content',
      sortKey: 'content',
      render: (r) => (
        <Mono isDim isTruncated>
          {r.content ?? '—'}
        </Mono>
      ),
    },
    { id: 'proxy', label: 'Proxy', sortKey: 'proxied', render: (r) => <ProxyStatus isProxied={r.proxied} />, isSecondary: true },
    sourceColumn,
  ],
  details: (r) =>
    withCommonSections(
      {
        title: 'Record',
        rows: [
          ['Type', <Chip>{r.record_type ?? '—'}</Chip>],
          ['Content', <Mono>{r.content ?? '—'}</Mono>],
          ['Proxy', <ProxyStatus isProxied={r.proxied} />],
          ['TTL', ttlLabel(r)],
        ],
      },
      r,
    ),
};

const tunnel: ResourceKind = {
  ...KIND_META.tunnel,
  description: 'Public hostnames routed through Cloudflare Tunnel to your containers.',
  searchFields: ['hostname', 'service', 'container_name'],
  searchPlaceholder: 'Search hostname, service, container',
  columns: [
    statusColumn,
    {
      id: 'hostname',
      label: 'Hostname',
      sortKey: 'hostname',
      render: (r) => (
        <HostnameCell
          hostname={r.hostname}
          extra={
            <>
              {r.path && <Chip>{r.path}</Chip>}
              <OpenAction hostname={r.hostname} />
            </>
          }
        />
      ),
    },
    { id: 'service', label: 'Service', sortKey: 'service', render: (r) => <ServiceURL service={r.service} /> },
    { id: 'access', label: 'Access', render: (r, index) => <AccessChip policy={policyFor(r, index)} /> },
    sourceColumn,
  ],
  details: (r, index) =>
    withCommonSections(
      {
        title: 'Ingress',
        rows: [
          ['Service', <ServiceURL service={r.service} />],
          ['Path', r.path ? <Mono>{r.path}</Mono> : 'All paths'],
          ['Tunnel ID', r.tunnel_id ? <Mono isDim>{r.tunnel_id}</Mono> : '—'],
          ['Access', <AccessChip policy={policyFor(r, index)} />],
        ],
      },
      r,
    ),
};

// The DNS or tunnel service whose access label created this Access app.
export function attachingKind(r: ManagedResource, index: ResourceIndex): 'tunnel' | 'dns' | undefined {
  if (index.byHostname.tunnel.has(r.hostname)) return 'tunnel';
  if (index.byHostname.dns.has(r.hostname)) return 'dns';
  return undefined;
}

const access: ResourceKind = {
  ...KIND_META.access,
  description: 'Zero Trust Access applications built from access policy labels.',
  searchFields: ['hostname', 'access_policy_name', 'access_app_name'],
  searchPlaceholder: 'Search hostname, policy, application',
  columns: [
    statusColumn,
    { id: 'hostname', label: 'Hostname', sortKey: 'hostname', render: (r) => <HostnameCell hostname={r.hostname} /> },
    { id: 'policy', label: 'Policy', sortKey: 'access_policy_name', render: (r) => <Mono>{r.access_policy_name ?? '—'}</Mono> },
    { id: 'decision', label: 'Decision', sortKey: 'access_decision', render: (r) => <DecisionPill decision={r.access_decision} /> },
    {
      id: 'application',
      label: 'Application',
      sortKey: 'access_app_name',
      render: (r) => <span className="lg-dim">{r.access_app_name ?? '—'}</span>,
      isSecondary: true,
    },
  ],
  details: (r, index) => {
    const owner = attachingKind(r, index);
    return withCommonSections(
      {
        title: 'Policy',
        rows: [
          ['Template', <Mono>{r.access_policy_name ?? '—'}</Mono>],
          ['Decision', <DecisionPill decision={r.access_decision} />],
          ['Application', r.access_app_name ?? '—'],
          ['App ID', r.access_app_id ? <Mono isDim>{r.access_app_id}</Mono> : '—'],
          ['Attached by', <Mono>{owner ? `${owner}.${r.service_name}` : r.service_name}</Mono>],
        ],
      },
      r,
    );
  },
};

export const RESOURCE_KINDS: Record<KindId, ResourceKind> = { dns, tunnel, access };
