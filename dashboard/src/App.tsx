import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { AppLayout } from './layouts/AppLayout';
import { Overview } from './pages/Overview';
import { Agents } from './pages/Agents';
import { ResourcePage } from './resources/ResourcePage';
import { LabelsPage } from './labels/LabelsPage';
import { AuthGate } from './auth/AuthGate';

export default function App() {
  return (
    <AuthGate>
      <BrowserRouter basename="/dashboard">
        <Routes>
          <Route element={<AppLayout />}>
            <Route index element={<Overview />} />
            <Route path="dns" element={<ResourcePage kind="dns" />} />
            <Route path="tunnels" element={<ResourcePage kind="tunnel" />} />
            <Route path="access" element={<ResourcePage kind="access" />} />
            <Route path="labels" element={<LabelsPage />} />
            <Route path="agents" element={<Agents />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </AuthGate>
  );
}
