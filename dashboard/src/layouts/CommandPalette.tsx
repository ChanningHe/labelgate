import { useMemo } from 'react';
import { useNavigate } from 'react-router-dom';
import { Spotlight, type SpotlightActionGroupData } from '@mantine/spotlight';
import { IconLayoutGrid, IconSearch, IconServer, IconTag } from '@tabler/icons-react';
import '@mantine/spotlight/styles.css';
import { KindIcon } from '../components/Badges';
import { RECIPES } from '../labels/catalog';
import { KIND_IDS, KIND_META } from '../resources/meta';
import { useResourceIndex } from '../resources/useResourceIndex';

const RESULT_LIMIT = 40;
const MAX_HEIGHT = 420;

// ⌘K / Ctrl+K: jump to a page, a managed hostname or a label recipe.
export function CommandPalette() {
  const navigate = useNavigate();
  const { lists } = useResourceIndex();
  const dns = lists.dns.data;
  const tunnel = lists.tunnel.data;
  const access = lists.access.data;

  const actions = useMemo<SpotlightActionGroupData[]>(() => {
    const resources = { dns, tunnel, access };
    return [
      {
        group: 'Pages',
        actions: [
          { id: 'page-overview', label: 'Overview', leftSection: <IconLayoutGrid size={16} />, onClick: () => navigate('/') },
          ...KIND_IDS.map((id) => {
            const meta = KIND_META[id];
            return {
              id: `page-${id}`,
              label: meta.title,
              leftSection: <KindIcon icon={meta.icon} color={meta.color} size={22} />,
              onClick: () => navigate(meta.path),
            };
          }),
          { id: 'page-labels', label: 'Labels', leftSection: <IconTag size={16} />, onClick: () => navigate('/labels') },
          { id: 'page-agents', label: 'Agents', leftSection: <IconServer size={16} />, onClick: () => navigate('/agents') },
        ],
      },
      {
        group: 'Resources',
        actions: KIND_IDS.flatMap((id) => {
          const meta = KIND_META[id];
          return (resources[id]?.resources ?? []).map((r) => ({
            id: `${id}-${r.id}`,
            label: r.hostname,
            description: `${meta.label} · ${r.container_name || r.service_name}`,
            leftSection: <KindIcon icon={meta.icon} color={meta.color} size={22} />,
            onClick: () => navigate(`${meta.path}?id=${encodeURIComponent(r.id)}`),
          }));
        }),
      },
      {
        group: 'Label recipes',
        actions: RECIPES.map((recipe) => {
          const meta = KIND_META[recipe.kind];
          return {
            id: `recipe-${recipe.id}`,
            label: recipe.title,
            description: `${meta.label} labels`,
            leftSection: <IconTag size={16} color={meta.color} />,
            onClick: () => navigate(`/labels?tab=${recipe.kind}`),
          };
        }),
      },
    ];
  }, [navigate, dns, tunnel, access]);

  return (
    <Spotlight
      actions={actions}
      shortcut="mod + K"
      limit={RESULT_LIMIT}
      scrollable
      maxHeight={MAX_HEIGHT}
      highlightQuery
      nothingFound="No pages, hostnames or recipes match"
      searchProps={{ leftSection: <IconSearch size={18} stroke={1.75} />, placeholder: 'Jump to a page, hostname or recipe' }}
    />
  );
}
