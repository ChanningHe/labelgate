import {
  Stack,
  Title,
  SimpleGrid,
  Card,
  Group,
  Text,
  Badge,
  ThemeIcon,
  Divider,
  Box,
  Skeleton,
  ActionIcon,
  Tooltip,
} from '@mantine/core';
import { useState } from 'react';
import {
  IconServer,
  IconWifi,
  IconWifiOff,
  IconWorldWww,
  IconStack2,
  IconEye,
  IconEyeOff,
} from '@tabler/icons-react';
import { StatusBadge } from '../components/StatusBadge';
import { useAgents } from '../hooks/useAPI';
import { mockAgents } from '../mock/data';
import { formatTime } from '../utils/format';

export function Agents() {
  const { data: apiData, error, isLoading } = useAgents();
  const [revealedIPs, setRevealedIPs] = useState<Set<string>>(new Set());

  const toggleIP = (agentId: string) =>
    setRevealedIPs((prev) => {
      const next = new Set(prev);
      next.has(agentId) ? next.delete(agentId) : next.add(agentId);
      return next;
    });

  // Only fall back to mock data in dev mode when the API is unreachable
  const useMock = !apiData && !!error && import.meta.env.DEV;
  const agents = useMock ? mockAgents : (apiData?.agents ?? []);

  if (isLoading && !apiData) {
    return (
      <Box maw={1200} mx="auto">
        <Stack gap="lg">
          <Title order={2}>Agents</Title>
          <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }}>
            {[1, 2, 3].map((i) => <Skeleton key={i} height={220} radius="md" />)}
          </SimpleGrid>
        </Stack>
      </Box>
    );
  }

  return (
    <Box maw={1200} mx="auto">
      <Stack gap="lg">
        <Title order={2}>Agents</Title>

        {agents.length === 0 ? (
          <Card withBorder p="xl" radius="md">
            <Text c="dimmed" ta="center">
              No agents configured. Agents report container data from remote Docker hosts.
            </Text>
          </Card>
        ) : (
          <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }}>
            {agents.map((agent: any) => (
              <Card key={agent.id} withBorder padding="lg" radius="md">
                <Group justify="space-between" mb="md">
                  <Group gap="sm">
                    <ThemeIcon
                      variant="light"
                      color={agent.connected ? 'green' : 'red'}
                      size="lg"
                      radius="md"
                    >
                      {agent.connected ? (
                        <IconWifi size={20} />
                      ) : (
                        <IconWifiOff size={20} />
                      )}
                    </ThemeIcon>
                    <div>
                      <Text fw={600} size="sm">
                        {agent.name || agent.id}
                      </Text>
                      <Text size="xs" c="dimmed" ff="monospace">
                        {agent.id}
                      </Text>
                    </div>
                  </Group>
                  <StatusBadge status={agent.status} />
                </Group>

                <Divider mb="md" />

                <Stack gap="xs">
                  <Group justify="space-between">
                    <Group gap={6}>
                      <IconWorldWww size={14} color="var(--mantine-color-dimmed)" />
                      <Text size="sm" c="dimmed">
                        Public IP
                      </Text>
                    </Group>
                    <Group gap={4}>
                      <Text
                        size="sm"
                        ff="monospace"
                        style={{
                          filter: revealedIPs.has(agent.id) ? 'none' : 'blur(4px)',
                          userSelect: revealedIPs.has(agent.id) ? 'text' : 'none',
                          transition: 'filter 0.2s',
                        }}
                      >
                        {agent.public_ip}
                      </Text>
                      <Tooltip label={revealedIPs.has(agent.id) ? 'Hide IP' : 'Show IP'} withArrow>
                        <ActionIcon
                          variant="subtle"
                          size="xs"
                          color="gray"
                          onClick={() => toggleIP(agent.id)}
                        >
                          {revealedIPs.has(agent.id) ? <IconEyeOff size={12} /> : <IconEye size={12} />}
                        </ActionIcon>
                      </Tooltip>
                    </Group>
                  </Group>

                  <Group justify="space-between">
                    <Group gap={6}>
                      <IconServer size={14} color="var(--mantine-color-dimmed)" />
                      <Text size="sm" c="dimmed">
                        Default Tunnel
                      </Text>
                    </Group>
                    <Badge variant="outline" size="sm" color="violet">
                      {agent.default_tunnel}
                    </Badge>
                  </Group>

                  <Group justify="space-between">
                    <Group gap={6}>
                      <IconStack2 size={14} color="var(--mantine-color-dimmed)" />
                      <Text size="sm" c="dimmed">
                        Resources
                      </Text>
                    </Group>
                    <Text size="sm" fw={500}>
                      {agent.resource_count}
                    </Text>
                  </Group>

                  <Group justify="space-between">
                    <Text size="sm" c="dimmed">
                      Last Seen
                    </Text>
                    <Text size="sm">
                      {formatTime(agent.last_seen)}
                    </Text>
                  </Group>

                  <Group justify="space-between">
                    <Text size="sm" c="dimmed">
                      Registered
                    </Text>
                    <Text size="sm">
                      {formatTime(agent.created_at)}
                    </Text>
                  </Group>
                </Stack>
              </Card>
            ))}
          </SimpleGrid>
        )}
      </Stack>
    </Box>
  );
}
