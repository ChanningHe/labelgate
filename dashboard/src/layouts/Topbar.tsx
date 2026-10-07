import type { ReactNode } from 'react';
import { ActionIcon, Tooltip, useComputedColorScheme, useMantineColorScheme } from '@mantine/core';
import { IconBook, IconBrandGithub, IconLogout, IconMenu2, IconMoon, IconSun, IconTag } from '@tabler/icons-react';
import { tokenStore } from '../api/token';
import classes from './AppLayout.module.css';

const DOCS_URL = 'https://labelgate-docs.pages.dev/';
const GITHUB_URL = 'https://github.com/channinghe/labelgate';

interface TopbarProps {
  onOpenNav: () => void;
  search?: ReactNode;
}

function forgetToken() {
  tokenStore.clear();
  // Reloading drops cached data; the next request gets 401 and shows the token prompt.
  window.location.reload();
}

export function Topbar({ onOpenNav, search }: TopbarProps) {
  const { setColorScheme } = useMantineColorScheme();
  const scheme = useComputedColorScheme('dark');
  const hasToken = tokenStore.read() !== null;
  const nextScheme = scheme === 'dark' ? 'light' : 'dark';

  return (
    <header className={classes.topbar}>
      <ActionIcon
        className={classes.burger}
        variant="subtle"
        color="gray"
        size="lg"
        onClick={onOpenNav}
        aria-label="Open navigation"
      >
        <IconMenu2 size={18} />
      </ActionIcon>
      <span className={classes.topbarBrand}>
        <span className={classes.logo}>
          <IconTag size={15} stroke={2.2} />
        </span>
        Labelgate
      </span>

      {search}

      <div className={classes.topbarActions}>
        <Tooltip label="Documentation" withArrow>
          <ActionIcon
            component="a"
            href={DOCS_URL}
            target="_blank"
            rel="noopener noreferrer"
            variant="subtle"
            color="gray"
            size="lg"
            aria-label="Documentation"
          >
            <IconBook size={18} stroke={1.75} />
          </ActionIcon>
        </Tooltip>
        <Tooltip label="GitHub" withArrow>
          <ActionIcon
            component="a"
            href={GITHUB_URL}
            target="_blank"
            rel="noopener noreferrer"
            variant="subtle"
            color="gray"
            size="lg"
            aria-label="GitHub repository"
          >
            <IconBrandGithub size={18} stroke={1.75} />
          </ActionIcon>
        </Tooltip>
        <Tooltip label={`Switch to ${nextScheme} mode`} withArrow>
          <ActionIcon
            variant="subtle"
            color="gray"
            size="lg"
            onClick={() => setColorScheme(nextScheme)}
            aria-label={`Switch to ${nextScheme} mode`}
          >
            {scheme === 'dark' ? <IconSun size={18} stroke={1.75} /> : <IconMoon size={18} stroke={1.75} />}
          </ActionIcon>
        </Tooltip>
        {hasToken && (
          <Tooltip label="Forget API token" withArrow>
            <ActionIcon variant="subtle" color="gray" size="lg" onClick={forgetToken} aria-label="Forget API token">
              <IconLogout size={18} stroke={1.75} />
            </ActionIcon>
          </Tooltip>
        )}
      </div>
    </header>
  );
}
