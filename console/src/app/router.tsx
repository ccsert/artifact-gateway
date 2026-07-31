import { createBrowserRouter } from 'react-router-dom';
import { AppLayout } from './Layout';
import { DashboardPage } from '../pages/Dashboard';
import { RepositoriesPage } from '../pages/Repositories';
import { RepositoryDetailPage } from '../pages/RepositoryDetail';
import { GroupsPage } from '../pages/Groups';
import { AuditsPage } from '../pages/Audits';
import { ApiKeysPage } from '../pages/ApiKeys';
import { AuditRetentionPage } from '../pages/AuditRetention';
import { ProxyGroupsPage } from '../pages/ProxyGroups';
import { SearchPage } from '../pages/Search';
import { LoginPage } from '../pages/Login';
import { AccessControlPage } from '../pages/AccessControl';

export const router = createBrowserRouter([
  { path: '/login', element: <LoginPage /> },
  {
    element: <AppLayout />,
    children: [
      { path: '/', element: <DashboardPage /> },
      { path: '/search', element: <SearchPage /> },
      { path: '/repositories', element: <RepositoriesPage /> },
      { path: '/repositories/:repositoryId', element: <RepositoryDetailPage /> },
      { path: '/groups', element: <GroupsPage /> },
      { path: '/access', element: <AccessControlPage /> },
      { path: '/proxy', element: <ProxyGroupsPage /> },
      { path: '/audits', element: <AuditsPage /> },
      { path: '/keys', element: <ApiKeysPage /> },
      { path: '/audit-retention', element: <AuditRetentionPage /> },
    ],
  },
]);
