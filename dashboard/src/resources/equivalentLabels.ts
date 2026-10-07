// Rebuilds the container labels that would produce a stored resource. The
// result is "equivalent", not the original: labelgate stores resolved values
// (target: auto becomes an IP) and does not keep every label, so comments mark
// what cannot be recovered.

import type { ManagedResource } from '../api/client';
import type { LabelLine } from '../labels/format';
import type { KindId } from './meta';
import { attachingKind, type ResourceIndex } from './useResourceIndex';

const DEFAULT_CREDENTIAL = 'default';
const AUTO_TTL = 1;
const DEFAULT_RECORD_TYPE = 'A';
const PROXIABLE_TYPES = new Set(['A', 'AAAA', 'CNAME']);
const RESOLVED_TARGET_TYPES = new Set(['A', 'AAAA']);

// Record-type fields that live in labels but not in labelgate's state.
const UNSTORED_FIELDS: Record<string, string> = {
  MX: 'priority',
  SRV: 'priority, weight and port',
  CAA: 'flags and tag',
};

type Entry = [key: string, value: string | undefined | false];

// Keeps entries with a value, so optional labels can be listed inline.
const present = (entries: Entry[]): LabelLine[] =>
  entries.flatMap(([key, value]) => (value ? [{ key, value }] : []));

const comment = (text: string): LabelLine => ({ comment: `# ${text}` });

const customCredential = (r: ManagedResource) =>
  r.credential && r.credential !== DEFAULT_CREDENTIAL ? r.credential : undefined;

const policyFor = (r: ManagedResource, index: ResourceIndex) =>
  index.byHostname.access.get(r.hostname)?.access_policy_name;

function dnsLabels(r: ManagedResource, index: ResourceIndex): LabelLine[] {
  const s = `dns.${r.service_name}`;
  const type = r.record_type ?? DEFAULT_RECORD_TYPE;
  const isProxiable = PROXIABLE_TYPES.has(type);
  const hasCustomTTL = !r.proxied && r.ttl !== undefined && r.ttl > AUTO_TTL;
  const unstored = UNSTORED_FIELDS[type];

  return [
    ...(RESOLVED_TARGET_TYPES.has(type)
      ? [comment('target shows the resolved address. Keep target: auto if that is what you wrote.')]
      : []),
    ...(unstored ? [comment(`${unstored} for ${type} records are not stored. Add the values you used.`)] : []),
    ...present([
      [`${s}.hostname`, r.hostname],
      [`${s}.type`, type !== DEFAULT_RECORD_TYPE && type],
      [`${s}.target`, r.content],
      [`${s}.proxied`, isProxiable && !r.proxied && 'false'],
      [`${s}.ttl`, hasCustomTTL && String(r.ttl)],
      [`${s}.credential`, customCredential(r)],
      [`${s}.access`, policyFor(r, index)],
      [`${s}.cleanup`, r.cleanup_enabled && 'true'],
    ]),
  ];
}

function tunnelLabels(r: ManagedResource, index: ResourceIndex): LabelLine[] {
  const s = `tunnel.${r.service_name}`;
  return present([
    [`${s}.hostname`, r.hostname],
    [`${s}.service`, r.service],
    [`${s}.path`, r.path],
    [`${s}.credential`, customCredential(r)],
    [`${s}.access`, policyFor(r, index)],
    [`${s}.cleanup`, r.cleanup_enabled && 'true'],
  ]);
}

function accessLabels(r: ManagedResource, index: ResourceIndex): LabelLine[] {
  const policy = r.access_policy_name || r.service_name;
  const p = `access.${policy}`;
  const owner = attachingKind(r, index) ?? 'tunnel';
  const defaultAppName = `labelgate-${policy}`;

  return [
    comment('Policy template'),
    ...present([
      [`${p}.policy.decision`, r.access_decision],
      [`${p}.app_name`, r.access_app_name !== defaultAppName && r.access_app_name],
    ]),
    comment('include, require and exclude rules are not stored. Copy them from your compose file.'),
    comment(`Attached by ${owner}.${r.service_name}`),
    { key: `${owner}.${r.service_name}.access`, value: policy },
  ];
}

const BUILDERS: Record<KindId, (r: ManagedResource, index: ResourceIndex) => LabelLine[]> = {
  dns: dnsLabels,
  tunnel: tunnelLabels,
  access: accessLabels,
};

export function equivalentLabels(kind: KindId, r: ManagedResource, index: ResourceIndex): LabelLine[] {
  return BUILDERS[kind](r, index);
}
