import { Outlet, useLocation } from 'react-router-dom';
import { useDisclosure } from '@mantine/hooks';
import { PageTransition } from '../components/PageTransition';
import { Sidebar } from './Sidebar';
import { Topbar } from './Topbar';
import classes from './AppLayout.module.css';

export function AppLayout() {
  const [isNavOpen, { open: openNav, close: closeNav }] = useDisclosure(false);
  const location = useLocation();

  return (
    <div className={classes.shell}>
      <Sidebar isOpen={isNavOpen} onNavigate={closeNav} />
      {isNavOpen && <div className={classes.backdrop} onClick={closeNav} aria-hidden="true" />}

      <div className={classes.main}>
        <Topbar onOpenNav={openNav} />
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
