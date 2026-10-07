import { m } from 'framer-motion';
import type { ReactNode } from 'react';

// Enter-only transition: a short fade and lift when the route changes.
// There is no exit animation, so navigation never waits on the old page.
const ENTER_DURATION_S = 0.18;
const EASE_OUT: [number, number, number, number] = [0.2, 0.8, 0.2, 1];

interface PageTransitionProps {
  routeKey: string;
  children: ReactNode;
}

export function PageTransition({ routeKey, children }: PageTransitionProps) {
  return (
    <m.div
      key={routeKey}
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: ENTER_DURATION_S, ease: EASE_OUT }}
    >
      {children}
    </m.div>
  );
}
