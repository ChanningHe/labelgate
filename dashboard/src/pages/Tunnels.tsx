import { useState, useMemo } from 'react';
import {
  Stack,
  Title,
  TextInput,
  SegmentedControl,
  Table,
  Group,
  Text,
  Code,
  Tooltip,
  Paper,
  Box,
  Skeleton,
  ActionIcon,
  Drawer,
  Grid,
  useMatches,
} from '@mantine/core';
import { IconSearch, IconChevronRight } from '@tabler/icons-react';
import { StatusBadge } from '../components/StatusBadge';
import { SortableHeader } from '../components/SortableHeader';
import { ResourceDetailContent } from '../components/ResourceDetailContent';
import { useTunnels } from '../hooks/useAPI';
import { mockTunnelResources } from '../mock/data';

type SortDirection = 'asc' | 'desc';
type SortColumn = 'hostname' | 'status' | 'container_name' | 'agent_id' | 'updated_at';

const statusOptions = [
  { label: 'All', value: 'all' },
  { label: 'Active', value: 'active' },
  { label: 'Orphaned', value: 'orphaned' },
  { label: 'Error', value: 'error' },
];

export function Tunnels() {
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState('all');
  const [sortColumn, setSortColumn] = useState<SortColumn>('hostname');
  const [sortDirection, setSortDirection] = useState<SortDirection>('asc');
  const [selectedResource, setSelectedResource] = useState<any>(null);

  const isMobile = useMatches({ base: true, md: false });

  const handleSort = (column: SortColumn) => {
    if (sortColumn === column) {
      setSortDirection(sortDirection === 'asc' ? 'desc' : 'asc');
    } else {
      setSortColumn(column);
      setSortDirection('asc');
    }
  };

  const handleRowClick = (resource: any) => {
    setSelectedResource(resource);
  };

  const queryParams: Record<string, string> = {};
  if (statusFilter !== 'all') queryParams.status = statusFilter;

  const { data: apiData, error, isLoading } = useTunnels(
    Object.keys(queryParams).length > 0 ? queryParams : undefined,
  );

  const useMock = !apiData && !!error && import.meta.env.DEV;
  const resources = useMock ? mockTunnelResources : (apiData?.resources ?? []);

  const filtered = useMemo(() => {
    let result = resources.filter((r: any) => {
      if (search && !r.hostname.toLowerCase().includes(search.toLowerCase())) return false;
      if (useMock && statusFilter !== 'all' && r.status !== statusFilter) return false;
      return true;
    });

    result = [...result].sort((a: any, b: any) => {
      let aValue = a[sortColumn];
      let bValue = b[sortColumn];
      if (aValue == null) aValue = '';
      if (bValue == null) bValue = '';
      aValue = String(aValue).toLowerCase();
      bValue = String(bValue).toLowerCase();
      if (aValue < bValue) return sortDirection === 'asc' ? -1 : 1;
      if (aValue > bValue) return sortDirection === 'asc' ? 1 : -1;
      return 0;
    });

    return result;
  }, [resources, search, statusFilter, useMock, sortColumn, sortDirection]);

  const tableContent = (
    <Paper withBorder radius="md" p={0}>
      <Group p="md" justify="space-between">
        <SegmentedControl
          data={statusOptions}
          value={statusFilter}
          onChange={setStatusFilter}
          size="sm"
        />
        <TextInput
          placeholder="Search hostname..."
          leftSection={<IconSearch size={16} />}
          value={search}
          onChange={(e) => setSearch(e.currentTarget.value)}
          style={{ width: isMobile ? 180 : 250 }}
          size="sm"
        />
      </Group>

      {isLoading && !apiData ? (
        <Stack p="md" gap="sm">
          {[1, 2, 3].map((i) => <Skeleton key={i} height={40} />)}
        </Stack>
      ) : (
        <Table.ScrollContainer minWidth={400}>
          <Table highlightOnHover verticalSpacing="sm">
            <Table.Thead>
              <Table.Tr>
                <SortableHeader
                  sorted={sortColumn === 'status'}
                  direction={sortColumn === 'status' ? sortDirection : null}
                  onSort={() => handleSort('status')}
                  style={{ width: 100 }}
                >
                  Status
                </SortableHeader>
                <SortableHeader
                  sorted={sortColumn === 'hostname'}
                  direction={sortColumn === 'hostname' ? sortDirection : null}
                  onSort={() => handleSort('hostname')}
                  style={{ maxWidth: 200 }}
                >
                  Hostname
                </SortableHeader>
                <Table.Th>Backend</Table.Th>
                {!isMobile && <Table.Th>Path</Table.Th>}
                {isMobile && <Table.Th style={{ width: 50 }}></Table.Th>}
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {filtered.length === 0 ? (
                <Table.Tr>
                  <Table.Td colSpan={isMobile ? 4 : 4}>
                    <Text c="dimmed" ta="center" py="xl">
                      No tunnel ingress rules found
                    </Text>
                  </Table.Td>
                </Table.Tr>
              ) : (
                filtered.map((r: any) => (
                  <Table.Tr
                    key={r.id}
                    onClick={() => handleRowClick(r)}
                    style={{
                      cursor: 'pointer',
                      borderLeft: !isMobile && selectedResource?.id === r.id
                        ? '3px solid var(--mantine-color-orange-filled)'
                        : '3px solid transparent',
                      backgroundColor: !isMobile && selectedResource?.id === r.id
                        ? 'var(--mantine-color-orange-light-hover)'
                        : undefined,
                    }}
                  >
                    <Table.Td>
                      {r.status === 'orphaned' ? (
                        <Tooltip
                          label={r.cleanup_enabled
                            ? 'Orphaned — will be cleaned up after delay'
                            : 'Orphaned — CF resource preserved (cleanup disabled)'}
                          withArrow multiline maw={400}
                        >
                          <span><StatusBadge status={r.status} iconOnly={isMobile} /></span>
                        </Tooltip>
                      ) : r.last_error ? (
                        <Tooltip label={r.last_error} withArrow multiline maw={400}>
                          <span><StatusBadge status={r.status} iconOnly={isMobile} /></span>
                        </Tooltip>
                      ) : (
                        <StatusBadge status={r.status} iconOnly={isMobile} />
                      )}
                    </Table.Td>
                    <Table.Td>
                      <Code>{r.hostname}</Code>
                    </Table.Td>
                    <Table.Td>
                      <Text
                        size="sm"
                        ff="monospace"
                      >
                        {r.service}
                      </Text>
                    </Table.Td>
                    {!isMobile && (
                      <Table.Td>
                        {r.path ? <Code>{r.path}</Code> : <Text size="sm" c="dimmed">/</Text>}
                      </Table.Td>
                    )}
                    {isMobile && (
                      <Table.Td>
                        <ActionIcon variant="subtle" color="gray">
                          <IconChevronRight size={16} />
                        </ActionIcon>
                      </Table.Td>
                    )}
                  </Table.Tr>
                ))
              )}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}

      <Group p="md" justify="space-between">
        <Text size="sm" c="dimmed">
          Showing {filtered.length} rules
        </Text>
      </Group>
    </Paper>
  );

  const desktopDetailPanel = (
    <Box pos="sticky" top={80}>
      <Paper withBorder radius="md" h="100%" style={{ minHeight: 400 }} p="lg">
        <ResourceDetailContent resource={selectedResource} type="tunnel" />
      </Paper>
    </Box>
  );

  const mobileDrawer = (
    <Drawer
      opened={!!selectedResource}
      onClose={() => setSelectedResource(null)}
      position="right"
      size="md"
      title="Tunnel Ingress Details"
    >
      <ResourceDetailContent resource={selectedResource} type="tunnel" showHeader={false} />
    </Drawer>
  );

  return (
    <Box maw={1400} mx="auto">
      <Stack gap="lg">
        <Title order={2}>Tunnel Ingress Rules</Title>

        {isMobile ? (
          <>
            {tableContent}
            {mobileDrawer}
          </>
        ) : (
          <Grid gutter="lg">
            <Grid.Col span={8}>{tableContent}</Grid.Col>
            <Grid.Col span={4}>{desktopDetailPanel}</Grid.Col>
          </Grid>
        )}
      </Stack>
    </Box>
  );
}
