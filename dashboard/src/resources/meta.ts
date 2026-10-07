import { IconBuildingTunnel, IconShieldLock, IconWorldWww, type Icon } from '@tabler/icons-react';

export type KindId = 'dns' | 'tunnel' | 'access';

// Key of each kind in the /api/overview resources object.
export type OverviewKey = 'dns' | 'tunnel_ingress' | 'access_app';

export interface KindMeta {
  id: KindId;
  label: string;
  title: string;
  noun: string;
  path: string;
  icon: Icon;
  color: string;
  overviewKey: OverviewKey;
}

export const KIND_IDS: readonly KindId[] = ['dns', 'tunnel', 'access'];

export const KIND_META: Record<KindId, KindMeta> = {
  dns: {
    id: 'dns',
    label: 'DNS',
    title: 'DNS Records',
    noun: 'record',
    path: '/dns',
    icon: IconWorldWww,
    color: 'var(--lg-dns)',
    overviewKey: 'dns',
  },
  tunnel: {
    id: 'tunnel',
    label: 'Tunnels',
    title: 'Tunnel Ingress',
    noun: 'ingress rule',
    path: '/tunnels',
    icon: IconBuildingTunnel,
    color: 'var(--lg-tunnel)',
    overviewKey: 'tunnel_ingress',
  },
  access: {
    id: 'access',
    label: 'Access',
    title: 'Access Policies',
    noun: 'access app',
    path: '/access',
    icon: IconShieldLock,
    color: 'var(--lg-access)',
    overviewKey: 'access_app',
  },
};
