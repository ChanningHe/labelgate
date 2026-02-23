import { Table, UnstyledButton, Group, Text, Center } from '@mantine/core';
import { IconChevronUp, IconChevronDown, IconSelector } from '@tabler/icons-react';
import type { CSSProperties } from 'react';

interface SortableHeaderProps {
  children: React.ReactNode;
  sorted: boolean;
  direction: 'asc' | 'desc' | null;
  onSort: () => void;
  style?: CSSProperties;
}

export function SortableHeader({ children, sorted, direction, onSort, style }: SortableHeaderProps) {
  const Icon = sorted ? (direction === 'asc' ? IconChevronUp : IconChevronDown) : IconSelector;

  return (
    //<Table.Th style={{ minWidth: 100, ...style }}>
    <Table.Th style={style}>
      <UnstyledButton onClick={onSort} w="100%">
        <Group justify="space-between" gap="xs" wrap="nowrap">
          <Text fw={500} size="sm">
            {children}
          </Text>
          <Center>
            <Icon size={14} stroke={1.5} />
          </Center>
        </Group>
      </UnstyledButton>
    </Table.Th>
  );
}
