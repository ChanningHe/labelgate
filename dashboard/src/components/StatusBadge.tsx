import { Badge, Tooltip, ThemeIcon } from '@mantine/core';
import {
  IconCircleCheck,
  IconAlertTriangle,
  IconCircleX,
  IconPlugConnected,
  IconPlugConnectedX,
} from '@tabler/icons-react';

const statusConfig: Record<string, { color: string; icon: React.ElementType; label: string }> = {
  active: { color: 'green', icon: IconCircleCheck, label: 'Active' },
  connected: { color: 'green', icon: IconPlugConnected, label: 'Connected' },
  orphaned: { color: 'yellow', icon: IconAlertTriangle, label: 'Orphaned' },
  deleted: { color: 'gray', icon: IconCircleX, label: 'Deleted' },
  disconnected: { color: 'red', icon: IconPlugConnectedX, label: 'Disconnected' },
  removed: { color: 'gray', icon: IconCircleX, label: 'Removed' },
  success: { color: 'green', icon: IconCircleCheck, label: 'Success' },
  error: { color: 'red', icon: IconCircleX, label: 'Error' },
};

interface StatusBadgeProps {
  status: string;
  iconOnly?: boolean;
}

export function StatusBadge({ status, iconOnly = false }: StatusBadgeProps) {
  const config = statusConfig[status] ?? { color: 'gray', icon: IconCircleX, label: status };
  const Icon = config.icon;

  if (iconOnly) {
    return (
      <Tooltip label={config.label} withArrow>
        <ThemeIcon
          color={config.color}
          variant="light"
          size="md"
          radius="xl"
          style={{ cursor: 'default' }}
        >
          <Icon size={16} />
        </ThemeIcon>
      </Tooltip>
    );
  }

  return (
    <Badge
      color={config.color}
      variant="light"
      size="sm"
      //leftSection={<Icon size={16} style={{ marginRight: 0 }} />}
    >
      {config.label}
    </Badge>
  );
}
