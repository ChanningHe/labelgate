import { Link } from 'react-router-dom';
import { ActionIcon, Drawer } from '@mantine/core';
import { IconArrowRight, IconX } from '@tabler/icons-react';
import { KindIcon } from '../components/Badges';
import { KIND_META, type KindId } from '../resources/meta';
import { recipesFor } from './catalog';
import { FormatSwitch } from './FormatSwitch';
import { RecipeCard } from './RecipeCard';
import classes from './LabelsPage.module.css';

const DRAWER_WIDTH = 560;

interface ExamplesDrawerProps {
  kind: KindId;
  isOpen: boolean;
  onClose: () => void;
}

// Recipes for one resource kind, opened from that kind's resource page.
export function ExamplesDrawer({ kind, isOpen, onClose }: ExamplesDrawerProps) {
  const meta = KIND_META[kind];

  return (
    <Drawer
      opened={isOpen}
      onClose={onClose}
      position="right"
      size={DRAWER_WIDTH}
      padding={0}
      withCloseButton={false}
      aria-label={`${meta.label} label examples`}
    >
      <div className={classes.drawerHead}>
        <KindIcon icon={meta.icon} color={meta.color} />
        <div>
          <h2>{meta.label} label examples</h2>
          <p>Copy a recipe and paste it under the service in compose.yaml.</p>
        </div>
        <ActionIcon variant="subtle" color="gray" onClick={onClose} aria-label="Close examples">
          <IconX size={16} />
        </ActionIcon>
      </div>
      <div className={classes.drawerBody}>
        <FormatSwitch />
        {recipesFor(kind).map((recipe, i) => (
          <RecipeCard key={recipe.id} recipe={recipe} order={i} />
        ))}
        <Link className={classes.link} to={`/labels?tab=${kind}`} onClick={onClose}>
          Open the full {meta.label} reference <IconArrowRight size={14} />
        </Link>
      </div>
    </Drawer>
  );
}
