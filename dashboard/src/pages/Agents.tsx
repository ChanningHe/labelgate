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
} from '@mantine/core';
import {
  IconServer,
  IconWifi,
  IconWifiOff,
  IconWorldWww,
  IconStack2,
} from '@tabler/icons-react';
import { StatusBadge } from '../components/StatusBadge';
import { useAgents } from '../hooks/useAPI';
import { mockAgents } from '../mock/data';
import { formatTime } from '../utils/format';

export function Agents() {
  const { data: apiData, error, isLoading } = useAgents();

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
                    <Text size="sm" ff="monospace">
                      {agent.public_ip}
                    </Text>
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
