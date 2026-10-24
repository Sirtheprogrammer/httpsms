import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom';
import { AuthProvider, useAuth } from './auth';
import { ConfirmProvider, Spinner, ToastProvider } from './components/ui';
import { Layout } from './components/Layout';
import { LoginPage } from './pages/Login';
import { LandingPage } from './pages/Landing';
import { OverviewPage } from './pages/Overview';
import { MessagesPage } from './pages/Messages';
import { SendPage } from './pages/Send';
import { DevicesPage } from './pages/Devices';
import { WorkflowsPage } from './pages/Workflows';
import { WorkflowEditorPage } from './workflow/Editor';
import { AIKeysPage } from './pages/AIKeys';
import { WebhooksPage } from './pages/Webhooks';
import { TokensPage } from './pages/Tokens';
import { AccountPage } from './pages/Account';
import { AdminUsersPage } from './pages/AdminUsers';
import { ReleasesPage } from './pages/Releases';
import './styles.css';

// Signed-out visitors see the public landing page at "/"; other pages ask them to sign in.
function Protected() {
  const { user, loading } = useAuth();
  const { pathname } = useLocation();
  if (loading) return <Spinner />;
  if (!user) return pathname === '/' ? <LandingPage /> : <Navigate to="/login" replace />;
  return <Layout />;
}

function AdminOnly({ children }: { children: JSX.Element }) {
  const { user } = useAuth();
  return user?.is_admin ? children : <Navigate to="/" replace />;
}

function App() {
  const { user, loading } = useAuth();
  return (
    <Routes>
      <Route path="/welcome" element={<LandingPage />} />
      <Route path="/login" element={loading ? <Spinner /> : user ? <Navigate to="/" replace /> : <LoginPage />} />
      <Route element={<Protected />}>
        <Route index element={<OverviewPage />} />
        <Route path="messages" element={<MessagesPage />} />
        <Route path="send" element={<SendPage />} />
        <Route path="devices" element={<DevicesPage />} />
        <Route path="workflows" element={<WorkflowsPage />} />
        <Route path="workflows/:id" element={<WorkflowEditorPage />} />
        <Route path="ai-keys" element={<AIKeysPage />} />
        <Route path="webhooks" element={<WebhooksPage />} />
        <Route path="api-tokens" element={<TokensPage />} />
        <Route path="account" element={<AccountPage />} />
        <Route path="admin/users" element={<AdminOnly><AdminUsersPage /></AdminOnly>} />
        <Route path="admin/releases" element={<AdminOnly><ReleasesPage /></AdminOnly>} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <ToastProvider>
        <ConfirmProvider>
          <AuthProvider>
            <App />
          </AuthProvider>
        </ConfirmProvider>
      </ToastProvider>
    </BrowserRouter>
  </StrictMode>,
);
