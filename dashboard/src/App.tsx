import { BrowserRouter, Routes, Route, Navigate, useLocation } from 'react-router-dom';
import { AnimatePresence } from 'framer-motion';
import { AppLayout } from './layouts/AppLayout';
import { Overview } from './pages/Overview';
import { DNS } from './pages/DNS';
import { Tunnels } from './pages/Tunnels';
import { Access } from './pages/Access';
import { Agents } from './pages/Agents';
import { PageTransition } from './components/PageTransition';

function AnimatedRoutes() {
  const location = useLocation();

  return (
    <AnimatePresence mode="wait">
      <Routes location={location} key={location.pathname}>
        <Route element={<AppLayout />}>
          <Route index element={<PageTransition><Overview /></PageTransition>} />
          <Route path="dns" element={<PageTransition><DNS /></PageTransition>} />
          <Route path="tunnels" element={<PageTransition><Tunnels /></PageTransition>} />
          <Route path="access" element={<PageTransition><Access /></PageTransition>} />
          <Route path="agents" element={<PageTransition><Agents /></PageTransition>} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Route>
      </Routes>
    </AnimatePresence>
  );
}

export default function App() {
  return (
    <BrowserRouter basename="/dashboard">
      <AnimatedRoutes />
    </BrowserRouter>
  );
}
