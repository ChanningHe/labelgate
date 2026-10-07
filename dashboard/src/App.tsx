import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { AppLayout } from './layouts/AppLayout';
import { Overview } from './pages/Overview';
import { DNS } from './pages/DNS';
import { Tunnels } from './pages/Tunnels';
import { Access } from './pages/Access';
import { Agents } from './pages/Agents';
import { AuthGate } from './auth/AuthGate';

export default function App() {
  return (
    <AuthGate>
      <BrowserRouter basename="/dashboard">
        <Routes>
          <Route element={<AppLayout />}>
            <Route index element={<Overview />} />
            <Route path="dns" element={<DNS />} />
            <Route path="tunnels" element={<Tunnels />} />
            <Route path="access" element={<Access />} />
            <Route path="agents" element={<Agents />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </AuthGate>
  );
}
