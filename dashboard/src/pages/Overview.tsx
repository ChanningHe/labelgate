import {
  SimpleGrid,
  Text,
  Group,
  Stack,
  ThemeIcon,
  Title,
  Paper,
  RingProgress,
  Center,
  Box,
  Skeleton,
} from '@mantine/core';
import {
  IconWorldWww,
  IconArrowsTransferDown,
  IconShieldLock,
  IconServer,
  IconRefresh,
  IconCloud,
  IconInfoCircle,
  IconCircleCheck,
  IconAlertTriangle,
  IconPlugConnected,
  IconPlugConnectedX,
  IconCircleX,
} from '@tabler/icons-react';
import { useNavigate } from 'react-router-dom';
import { useOverview } from '../hooks/useAPI';
import { mockOverview, type OverviewData } from '../mock/data';
import { formatTime } from '../utils/format';

interface StatCardProps {
  title: string;
  icon: React.ElementType;
  color: string;
  total: number;
  breakdowns: { label: string; value: number; color: string; icon: React.ElementType }[];
  to?: string;
}

function StatCard({ title, icon: Icon, color, total, breakdowns, to }: StatCardProps) {
  const navigate = useNavigate();

  const handleClick = () => {
    if (to) {
      navigate(to);
    }
  };
  const sections = breakdowns
    .filter((b) => b.value > 0 && total > 0)
    .map((b) => ({
      value: Math.round((b.value / total) * 100),
      color: b.color,
      tooltip: `${b.label}: ${b.value}`,
    }));

  return (
    <Paper
      withBorder
      p="xl"
      radius="md"
      onClick={handleClick}
      style={{
        cursor: to ? 'pointer' : 'default',
        transition: 'transform 0.15s ease, box-shadow 0.15s ease',
      }}
      onMouseEnter={(e) => {
        if (to) {
          e.currentTarget.style.transform = 'translateY(-2px)';
          e.currentTarget.style.boxShadow = 'var(--mantine-shadow-md)';
        }
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.transform = 'translateY(0)';
        e.currentTarget.style.boxShadow = 'none';
      }}
    >
      <Group justify="space-between" mb="lg">
        <Text fw={700} size="lg">
          {title}
        </Text>
      </Group>
      <Group wrap="nowrap" gap="xl">
        <RingProgress
          roundCaps
          thickness={8}
          size={120}
          sections={sections.length > 0 ? sections : [{ value: 100, color: 'gray.3' }]}
          label={
            <Center>
              <ThemeIcon variant="light" color={color} size={48} radius="xl">
                <Icon size={26} />
              </ThemeIcon>
            </Center>
          }
        />
        <Stack gap="sm" style={{ flex: 1 }}>
          {breakdowns.map((b) => {
            const BIcon = b.icon;
            return (
              <Group key={b.label} justify="space-between">
                <Group gap="xs">
                  <ThemeIcon variant="light" color={b.color} size="sm" radius="xl">
                    <BIcon size={12} />
                  </ThemeIcon>
                  <div>
                    <Text size="sm" fw={500}>
                      {b.label}
                    </Text>
                    <Text size="xs" c="dimmed">
                      {total > 0 ? Math.round((b.value / total) * 100) : 0}%
                    </Text>
                  </div>
                </Group>
                <Text fw={700} size="lg">
                  {b.value}
                </Text>
              </Group>
            );
          })}
        </Stack>
      </Group>
    </Paper>
  );
}

export function Overview() {
  const { data: apiData, error, isLoading } = useOverview();

  // Only fall back to mock data in dev mode when the API is unreachable
  const useMock = !apiData && !!error && import.meta.env.DEV;
  const emptyOverview: OverviewData = {
    resources: {
      dns: { total: 0, active: 0, orphaned: 0, error: 0 },
      tunnel_ingress: { total: 0, active: 0, orphaned: 0, error: 0 },
      access_app: { total: 0, active: 0, orphaned: 0, error: 0 },
    },
    agents: { total: 0, connected: 0, disconnected: 0 },
    sync: { last_sync: '', status: 'success', error: '' },
    cloudflare: { reachable: false, last_check: '' },
    version: '',
    uptime: '',
    started_at: '',
  };
  const data: OverviewData = useMock ? mockOverview : (apiData ?? emptyOverview);

  if (isLoading && !apiData) {
    return (
      <Box maw={1200} mx="auto">
        <Stack gap="xl">
          <Title order={2}>Overview</Title>
          <SimpleGrid cols={{ base: 1, md: 3 }}>
            {[1, 2, 3].map((i) => <Skeleton key={i} height={200} radius="md" />)}
          </SimpleGrid>
        </Stack>
      </Box>
    );
  }

  return (
    <Box maw={1200} mx="auto">
      <Stack gap="xl">
        <Title order={2}>Overview</Title>

        {/* Resources section */}
        <div>
          <Text size="sm" fw={600} c="dimmed" tt="uppercase" mb="md">
            Resources
          </Text>
          <SimpleGrid cols={{ base: 1, md: 3 }}>
            <StatCard
              title="DNS Records"
              icon={IconWorldWww}
              color="blue"
              total={data.resources.dns.total}
              to="/dns"
              breakdowns={[
                { label: 'Active', value: data.resources.dns.active, color: 'green', icon: IconCircleCheck },
                { label: 'Orphaned', value: data.resources.dns.orphaned, color: 'yellow', icon: IconAlertTriangle },
                { label: 'Error', value: data.resources.dns.error, color: 'red', icon: IconCircleX },
              ]}
            />
            <StatCard
              title="Tunnel Ingress"
              icon={IconArrowsTransferDown}
              color="violet"
              total={data.resources.tunnel_ingress.total}
              to="/tunnels"
              breakdowns={[
                { label: 'Active', value: data.resources.tunnel_ingress.active, color: 'green', icon: IconCircleCheck },
                { label: 'Orphaned', value: data.resources.tunnel_ingress.orphaned, color: 'yellow', icon: IconAlertTriangle },
                { label: 'Error', value: data.resources.tunnel_ingress.error, color: 'red', icon: IconCircleX },
              ]}
            />
            <StatCard
              title="Access Policies"
              icon={IconShieldLock}
              color="teal"
              total={data.resources.access_app.total}
              to="/access"
              breakdowns={[
                { label: 'Active', value: data.resources.access_app.active, color: 'green', icon: IconCircleCheck },
                { label: 'Orphaned', value: data.resources.access_app.orphaned, color: 'yellow', icon: IconAlertTriangle },
                { label: 'Error', value: data.resources.access_app.error, color: 'red', icon: IconCircleX },
              ]}
            />
          </SimpleGrid>
        </div>

        {/* Agents section */}
        <div>
          <Text size="sm" fw={600} c="dimmed" tt="uppercase" mb="md">
            Agents
          </Text>
          <SimpleGrid cols={{ base: 1, md: 3 }}>
            <StatCard
              title="Agents"
              icon={IconServer}
              color="orange"
              total={data.agents.total}
              to="/agents"
              breakdowns={[
                { label: 'Connected', value: data.agents.connected, color: 'green', icon: IconPlugConnected },
                { label: 'Disconnected', value: data.agents.disconnected, color: 'red', icon: IconPlugConnectedX },
              ]}
            />
          </SimpleGrid>
        </div>

        {/* Health section */}
        <div>
          <Text size="sm" fw={600} c="dimmed" tt="uppercase" mb="md">
            Health
          </Text>
          <SimpleGrid cols={{ base: 1, md: 3 }}>
            <Paper withBorder p="xl" radius="md">
              <Group gap="sm" mb="lg">
                <ThemeIcon variant="light" color="blue" size="md" radius="md">
                  <IconRefresh size={18} />
                </ThemeIcon>
                <Text fw={700} size="lg">
                  Last Sync
                </Text>
              </Group>
              <Stack gap="sm">
                <Group justify="space-between">
                  <Text size="sm" c="dimmed">Time</Text>
                  <Text size="sm">{formatTime(data.sync.last_sync)}</Text>
                </Group>
                <Group justify="space-between">
                  <Text size="sm" c="dimmed">Status</Text>
                  <Group gap={6}>
                    <ThemeIcon
                      variant="light"
                      color={data.sync.status === 'success' ? 'green' : 'red'}
                      size="sm"
                      radius="xl"
                    >
                      {data.sync.status === 'success' ? (
                        <IconCircleCheck size={12} />
                      ) : (
                        <IconAlertTriangle size={12} />
                      )}
                    </ThemeIcon>
                    <Text size="sm" fw={500}>
                      {data.sync.status}
                    </Text>
                  </Group>
                </Group>
                {data.sync.error && (
                  <Text size="xs" c="red">
                    {data.sync.error}
                  </Text>
                )}
              </Stack>
            </Paper>

            <Paper withBorder p="xl" radius="md">
              <Group gap="sm" mb="lg">
                <ThemeIcon variant="light" color="orange" size="md" radius="md">
                  <IconCloud size={18} />
                </ThemeIcon>
                <Text fw={700} size="lg">
                  Cloudflare API
                </Text>
              </Group>
              <Stack gap="sm">
                <Group justify="space-between">
                  <Text size="sm" c="dimmed">Last Check</Text>
                  <Text size="sm">{formatTime(data.cloudflare.last_check)}</Text>
                </Group>
                <Group justify="space-between">
                  <Text size="sm" c="dimmed">Status</Text>
                  <Group gap={6}>
                    <ThemeIcon
                      variant="light"
                      color={data.cloudflare.reachable ? 'green' : 'red'}
                      size="sm"
                      radius="xl"
                    >
                      {data.cloudflare.reachable ? (
                        <IconCircleCheck size={12} />
                      ) : (
                        <IconAlertTriangle size={12} />
                      )}
                    </ThemeIcon>
                    <Text size="sm" fw={500}>
                      {data.cloudflare.reachable ? 'Reachable' : 'Unreachable'}
                    </Text>
                  </Group>
                </Group>
              </Stack>
            </Paper>

            <Paper withBorder p="xl" radius="md">
              <Group gap="sm" mb="lg">
                <ThemeIcon variant="light" color="gray" size="md" radius="md">
                  <IconInfoCircle size={18} />
                </ThemeIcon>
                <Text fw={700} size="lg">
                  System
                </Text>
              </Group>
              <Stack gap="sm">
                <Group justify="space-between">
                  <Text size="sm" c="dimmed">Version</Text>
                  <Text size="sm" fw={500} ff="monospace">
                    v{data.version}
                  </Text>
                </Group>
                <Group justify="space-between">
                  <Text size="sm" c="dimmed">Uptime</Text>
                  <Text size="sm" fw={500}>
                    {data.uptime}
                  </Text>
                </Group>
                <Group justify="space-between">
                  <Text size="sm" c="dimmed">Started</Text>
                  <Text size="sm">
                    {formatTime(data.started_at)}
                  </Text>
                </Group>
              </Stack>
            </Paper>
          </SimpleGrid>
        </div>
      </Stack>
    </Box>
  );
}
