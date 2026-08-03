import { useEffect, useState } from 'react';
import {
  ClockCircleOutlined,
  DashboardOutlined,
  FileSearchOutlined,
  InboxOutlined,
  KeyOutlined,
  LoginOutlined,
  SafetyCertificateOutlined,
  SearchOutlined,
  TeamOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { Button, Input, Menu, Space } from 'antd';
import type { MenuProps } from 'antd';
import { Link, Navigate, Outlet, useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { useAuth } from '../lib/auth';
import { Modal, useDisclosure } from '../components/Modal';
import { Field } from '../components/Layout';

const navItems = [
  { to: '/', label: '总览', exact: true, icon: <DashboardOutlined />, group: '运行' },
  { to: '/repositories', label: '仓库', icon: <InboxOutlined />, group: '运行' },
  { to: '/search', label: '制品搜索', icon: <SearchOutlined />, group: '运行' },
  { to: '/groups', label: '分组', icon: <TeamOutlined />, group: '治理' },
  { to: '/access', label: '访问控制', icon: <SafetyCertificateOutlined />, group: '治理' },
  { to: '/audits', label: '审计日志', icon: <FileSearchOutlined />, group: '治理' },
  { to: '/audit-retention', label: '审计保留', icon: <ClockCircleOutlined />, group: '治理' },
  { to: '/keys', label: 'API 密钥', icon: <KeyOutlined />, group: '管理', admin: true },
  { to: '/users', label: '用户', icon: <UserOutlined />, group: '管理', admin: true },
] as const;

function GlobalSearchBox() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const [value, setValue] = useState(params.get('q') ?? '');

  useEffect(() => {
    setValue(params.get('q') ?? '');
  }, [params]);

  const search = (nextValue: string) => {
    const query = nextValue.trim();
    if (query) navigate(`/search?q=${encodeURIComponent(query)}`);
  };

  return (
    <Input.Search
      allowClear
      className="w-full max-w-md"
      placeholder="跨仓库搜索制品…"
      value={value}
      onChange={(event) => setValue(event.target.value)}
      onSearch={search}
    />
  );
}

function TokenDialog() {
  const { token, setToken, clearToken } = useAuth();
  const dialog = useDisclosure();
  const [draft, setDraft] = useState('');

  return (
    <>
      <Button
        color={token ? 'green' : 'orange'}
        variant="filled"
        icon={<KeyOutlined />}
        onClick={() => {
          setDraft(token);
          dialog.show();
        }}
      >
        {token ? '已配置 Token' : '设置 Token'}
      </Button>
      <Modal
        open={dialog.open}
        title="API 访问令牌"
        onClose={dialog.hide}
        footer={
          <div className="flex items-center justify-between gap-4">
            <div>
              {token && (
                <Button
                  danger
                  onClick={() => {
                    clearToken();
                    dialog.hide();
                  }}
                >
                  清除令牌
                </Button>
              )}
            </div>
            <Space>
              <Button onClick={dialog.hide}>取消</Button>
              <Button
                type="primary"
                disabled={!draft.trim()}
                onClick={() => {
                  setToken(draft);
                  dialog.hide();
                }}
              >
                保存
              </Button>
            </Space>
          </div>
        }
      >
        <Field label="Bearer Token" hint="管理 API 使用 Bearer 认证，令牌仅保存在浏览器 localStorage。">
          <Input.TextArea
            className="font-mono text-xs"
            autoSize={{ minRows: 4, maxRows: 8 }}
            placeholder="粘贴 Token…"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
          />
        </Field>
      </Modal>
    </>
  );
}

export function AppLayout() {
  const { token, role, clearToken } = useAuth();
  const location = useLocation();

  if (!token) {
    if (location.pathname === '/' || location.pathname === '/search') {
      return <Navigate to={`/browse${location.search}`} replace />;
    }
    const target = encodeURIComponent(location.pathname + location.search);
    return <Navigate to={`/login?redirect=${target}`} replace />;
  }

  // Static admin and OIDC tokens do not always carry a locally persisted role.
  // Keep the management entries discoverable and let the API enforce authority.
  const visibleNavItems = navItems.filter((item) => !('admin' in item) || role === 'admin' || role === '');
  const selectedItem = visibleNavItems.find((item) =>
    'exact' in item ? location.pathname === item.to : location.pathname.startsWith(item.to),
  );
  const menuItems: MenuProps['items'] = (['运行', '治理', '管理'] as const).flatMap((group) => {
    const children = visibleNavItems
      .filter((item) => item.group === group)
      .map((item) => ({
        key: item.to,
        icon: item.icon,
        label: <Link to={item.to}>{item.label}</Link>,
      }));
    return children.length > 0
      ? [{ key: `group-${group}`, type: 'group' as const, label: group, children }]
      : [];
  });

  return (
    <div className="flex min-h-screen">
      <aside className="fixed inset-y-0 left-0 z-30 flex w-56 flex-col border-r border-zinc-800/80 bg-zinc-900/45">
        <div className="flex items-center gap-2.5 px-5 py-5">
          <div className="flex h-8 w-8 items-center justify-center rounded-md bg-cyan-600 text-sm font-bold text-white">
            AG
          </div>
          <div>
            <div className="text-sm font-semibold text-zinc-100">Artifact Gateway</div>
            <div className="text-[10px] uppercase tracking-widest text-zinc-600">Console</div>
          </div>
        </div>
        <Menu
          className="flex-1 border-0 bg-transparent px-2"
          mode="inline"
          theme="dark"
          selectedKeys={selectedItem ? [selectedItem.to] : []}
          items={menuItems}
        />
        <div className="border-t border-zinc-800/80 px-4 py-3 text-[10px] leading-4 text-zinc-600">
          Native Hosted API v2
        </div>
      </aside>
      <div className="ml-56 flex min-h-screen min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-20 flex items-center gap-3 border-b border-zinc-800/80 bg-zinc-950/90 px-6 py-3 backdrop-blur">
          <GlobalSearchBox />
          <Space className="ml-auto">
            <TokenDialog />
            <Button icon={<LoginOutlined />} onClick={clearToken}>
              退出
            </Button>
          </Space>
        </header>
        <main className="flex-1 px-6 py-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
