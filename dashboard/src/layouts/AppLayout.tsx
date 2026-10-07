import { Outlet, useLocation } from 'react-router-dom';
import { useDisclosure } from '@mantine/hooks';
import { PageTransition } from '../components/PageTransition';
import { spotlight } from '@mantine/spotlight';
import { IconSearch } from '@tabler/icons-react';
import { CommandPalette } from './CommandPalette';
import { Sidebar } from './Sidebar';
import { Topbar } from './Topbar';
import classes from './AppLayout.module.css';

const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform);

function SearchButton() {
  return (
    <button type="button" className={classes.search} onClick={spotlight.open} aria-label="Search">
      <IconSearch size={16} stroke={1.75} />
      <span className={classes.searchText}>Search hostnames, containers, label recipes…</span>
      <kbd className={classes.kbd}>{isMac ? '⌘K' : 'Ctrl K'}</kbd>
    </button>
  );
}

export function AppLayout() {
  const [isNavOpen, { open: openNav, close: closeNav }] = useDisclosure(false);
  const location = useLocation();

  return (
    <div className={classes.shell}>
      <CommandPalette />
      <Sidebar isOpen={isNavOpen} onNavigate={closeNav} />
      {isNavOpen && <div className={classes.backdrop} onClick={closeNav} aria-hidden="true" />}

      <div className={classes.main}>
        <Topbar onOpenNav={openNav} search={<SearchButton />} />
        <main className={classes.content}>
          <div className={classes.page}>
            <PageTransition routeKey={location.pathname}>
              <Outlet />
            </PageTransition>
          </div>
        </main>
      </div>
    </div>
  );
}
