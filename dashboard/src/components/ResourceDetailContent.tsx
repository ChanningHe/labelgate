import {
  Title,
  Text,
  Group,
  Stack,
  Badge,
  Code,
  Divider,
  ThemeIcon,
  Box,
  Tooltip,
} from '@mantine/core';
import {
  IconWorldWww,
  IconArrowsTransferDown,
  IconShieldLock,
  IconInfoCircle,
  IconSettings,
  IconClock,
} from '@tabler/icons-react';
import { StatusBadge } from './StatusBadge';
import { formatTime } from '../utils/format';

export type ResourceType = 'dns' | 'tunnel' | 'access';

interface BaseResource {
  id: string;
  hostname: string;
  status: string;
  service_name?: string;
  container_id?: string;
  container_name?: string;
  agent_id?: string;
  created_at?: string;
  updated_at?: string;
  last_error?: string;
  cleanup_enabled?: boolean;
}

interface DNSResource extends BaseResource {
  record_type: string;
  content: string;
  proxied: boolean;
  ttl?: number;
}

interface TunnelResource extends BaseResource {
  service: string;
  tunnel_id?: string;
  path?: string;
}

interface AccessResource extends BaseResource {
  app_name?: string;
  policies?: string[];
}

type Resource = DNSResource | TunnelResource | AccessResource;

interface ResourceDetailContentProps {
  resource: Resource | null;
  type: ResourceType;
  showHeader?: boolean;
}

export function getResourceTypeInfo(type: ResourceType) {
  switch (type) {
    case 'dns':
      return { label: 'DNS Record', icon: IconWorldWww, color: 'blue' };
    case 'tunnel':
      return { label: 'Tunnel Ingress', icon: IconArrowsTransferDown, color: 'violet' };
    case 'access':
      return { label: 'Access Policy', icon: IconShieldLock, color: 'teal' };
  }
}

function DetailItem({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <Group justify="space-between" wrap="nowrap">
      <Text size="sm" c="dimmed">
        {label}
      </Text>
      <Box style={{ maxWidth: '60%', textAlign: 'right' }}>{value}</Box>
    </Group>
  );
}

function DetailSection({
  icon: Icon,
  title,
  children,
}: {
  icon: React.ElementType;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <Stack gap="sm">
      <Group gap="xs">
        <ThemeIcon variant="light" size="sm" radius="md">
          <Icon size={14} />
        </ThemeIcon>
        <Text fw={600} size="sm">
          {title}
        </Text>
      </Group>
      <Stack gap="xs" pl="lg">
        {children}
      </Stack>
    </Stack>
  );
}

// Empty state for when no resource is selected
export function ResourceDetailEmpty({ type }: { type: ResourceType }) {
  const typeInfo = getResourceTypeInfo(type);
  const TypeIcon = typeInfo.icon;

  return (
    <Stack gap="md" align="center" justify="center" h="100%" py="xl">
      <ThemeIcon variant="light" color="gray" size={60} radius="xl">
        <TypeIcon size={30} />
      </ThemeIcon>
      <Stack gap={4} align="center">
        <Text size="sm" fw={500} c="dimmed">
          No {typeInfo.label} Selected
        </Text>
        <Text size="xs" c="dimmed">
          Click a row to view details
        </Text>
      </Stack>
    </Stack>
  );
}

// Main content component - just the content, no container
export function ResourceDetailContent({ resource, type, showHeader = true }: ResourceDetailContentProps) {
  const typeInfo = getResourceTypeInfo(type);
  const TypeIcon = typeInfo.icon;

  if (!resource) {
    return <ResourceDetailEmpty type={type} />;
  }

  return (
    <Stack gap="lg">
      {/* Header (optional) */}
      {showHeader && (
        <>
          <Group gap="sm">
            <ThemeIcon variant="light" color={typeInfo.color} size="lg" radius="md">
              <TypeIcon size={20} />
            </ThemeIcon>
            <div>
              <Title order={5}>{typeInfo.label}</Title>
              <Text size="xs" c="dimmed">
                Resource Details
              </Text>
            </div>
          </Group>
          <Divider />
        </>
      )}

      {/* Status Section */}
      <Box>
        <Group justify="space-between">
          <Text fw={500} size="sm">Status</Text>
          {resource.last_error ? (
            <Group gap="xs">
              <StatusBadge status={resource.status} />
            </Group>
          ) : (
            <StatusBadge status={resource.status} />
          )}
        </Group>
        {resource.last_error && (
          <Text size="xs" c="red" mt="xs">
            {resource.last_error}
          </Text>
        )}
      </Box>

      <Divider />

      {/* Basic Info Section */}
      <DetailSection icon={IconInfoCircle} title="Basic Information">
        <DetailItem label="Hostname" value={<Code>{resource.hostname}</Code>} />
        <DetailItem label="Service" value={resource.service_name || '—'} />
        <DetailItem
          label="Container"
          value={
            resource.container_name ? (
              <Tooltip label={resource.container_id} withArrow position="left">
                <Text size="sm">{resource.container_name}</Text>
              </Tooltip>
            ) : (
              '—'
            )
          }
        />
        <DetailItem label="Agent" value={resource.agent_id || 'local'} />
      </DetailSection>

      <Divider />

      {/* Resource-specific Section */}
      <DetailSection icon={IconSettings} title="Configuration">
        {type === 'dns' && (
          <>
            <DetailItem
              label="Record Type"
              value={
                <Badge variant="outline" size="sm" color="gray">
                  {(resource as DNSResource).record_type}
                </Badge>
              }
            />
            <DetailItem
              label="Content"
              value={<Code>{(resource as DNSResource).content}</Code>}
            />
            <DetailItem
              label="Proxied"
              value={
                <Badge
                  variant="light"
                  size="xs"
                  color={(resource as DNSResource).proxied ? 'orange' : 'gray'}
                >
                  {(resource as DNSResource).proxied ? 'Yes' : 'No'}
                </Badge>
              }
            />
            <DetailItem label="TTL" value={(resource as DNSResource).ttl || 'Auto'} />
          </>
        )}
        {type === 'tunnel' && (
          <>
            <DetailItem
              label="Backend"
              value={<Code>{(resource as TunnelResource).service}</Code>}
            />
            <DetailItem
              label="Tunnel ID"
              value={
                (resource as TunnelResource).tunnel_id ? (
                  <Badge variant="outline" size="sm" color="violet">
                    {(resource as TunnelResource).tunnel_id}
                  </Badge>
                ) : (
                  '—'
                )
              }
            />
            <DetailItem
              label="Path"
              value={
                (resource as TunnelResource).path ? (
                  <Code>{(resource as TunnelResource).path}</Code>
                ) : (
                  '/'
                )
              }
            />
          </>
        )}
        {type === 'access' && (
          <>
            <DetailItem label="App Name" value={(resource as AccessResource).app_name || '—'} />
            <DetailItem
              label="Policies"
              value={
                (resource as AccessResource).policies &&
                (resource as AccessResource).policies!.length > 0 ? (
                  <Stack gap={4}>
                    {(resource as AccessResource).policies!.map((p, i) => (
                      <Badge key={i} variant="light" size="sm" color="teal">
                        {p}
                      </Badge>
                    ))}
                  </Stack>
                ) : (
                  '—'
                )
              }
            />
          </>
        )}
      </DetailSection>

      <Divider />

      {/* Metadata Section */}
      <DetailSection icon={IconClock} title="Metadata">
        <DetailItem
          label="Created"
          value={
            <Text size="sm">{resource.created_at ? formatTime(resource.created_at) : '—'}</Text>
          }
        />
        <DetailItem
          label="Updated"
          value={
            <Text size="sm">{resource.updated_at ? formatTime(resource.updated_at) : '—'}</Text>
          }
        />
        <DetailItem
          label="Cleanup"
          value={
            <Badge variant="light" size="xs" color={resource.cleanup_enabled ? 'green' : 'gray'}>
              {resource.cleanup_enabled ? 'Enabled' : 'Disabled'}
            </Badge>
          }
        />
      </DetailSection>
    </Stack>
  );
}
