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

export const router = createBrowserRouter([
  {
    element: <AppLayout />,
    children: [
      { path: '/', element: <DashboardPage /> },
      { path: '/repositories', element: <RepositoriesPage /> },
      { path: '/repositories/:repositoryId', element: <RepositoryDetailPage /> },
      { path: '/groups', element: <GroupsPage /> },
      { path: '/proxy', element: <ProxyGroupsPage /> },
      { path: '/audits', element: <AuditsPage /> },
      { path: '/keys', element: <ApiKeysPage /> },
      { path: '/audit-retention', element: <AuditRetentionPage /> },
    ],
  },
]);
