import { useLocation, useNavigate } from 'react-router-dom';

import {
  AppstoreOutlined,
  BarChartOutlined,
  ContainerOutlined,
  DatabaseOutlined,
  ExperimentOutlined,
  FolderOutlined,
  HomeOutlined,
  OrderedListOutlined,
  PlayCircleOutlined,
  SettingOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { Menu, type MenuProps } from 'antd';

import { useAuthStore } from '@/store/auth';

import './MainSidebarContent.css';

interface MainSidebarContentProps {
  onNavigate?: () => void;
}

/**
 * Main sidebar content component.
 * Shows project sub-navigation when inside a project context.
 */
const MainSidebarContent: React.FC<MainSidebarContentProps> = ({
  onNavigate,
}) => {
  const navigate = useNavigate();
  const location = useLocation();
  const { user } = useAuthStore();

  const isAdmin = !!user?.is_superuser;

  // Detect if we're inside a project context
  const projectMatch = location.pathname.match(/^\/projects\/(\d+)/);
  const projectId = projectMatch ? projectMatch[1] : null;

  // Build menu items
  const menuItems: MenuProps['items'] = [
    {
      key: '/home',
      icon: <HomeOutlined />,
      label: 'Home',
    },
    {
      key: '/projects',
      icon: <FolderOutlined />,
      label: 'Projects',
    },
    // Project sub-nav (when inside a project)
    ...(projectId
      ? [
          {
            type: 'divider' as const,
            style: { margin: '8px 0' },
          },
          {
            key: 'project-header',
            type: 'group' as const,
            label: (
              <span
                style={{
                  fontWeight: 500,
                  color: 'var(--color-text-primary)',
                  padding: '0 8px',
                  fontSize: 12,
                  textTransform: 'uppercase' as const,
                  letterSpacing: '0.5px',
                }}
              >
                Project
              </span>
            ),
          },
          {
            key: `/projects/${projectId}`,
            icon: <AppstoreOutlined />,
            label: 'Overview',
          },
          {
            key: `/projects/${projectId}/datapacks`,
            icon: <ExperimentOutlined />,
            label: 'Datapacks',
          },
          {
            key: `/projects/${projectId}/executions`,
            icon: <PlayCircleOutlined />,
            label: 'Executions',
          },
          {
            key: `/projects/${projectId}/evaluations`,
            icon: <BarChartOutlined />,
            label: 'Evaluations',
          },
          {
            key: `/projects/${projectId}/settings`,
            icon: <SettingOutlined />,
            label: 'Settings',
          },
        ]
      : []),
    {
      type: 'divider' as const,
      style: { margin: '8px 0' },
    },
    {
      key: '/tasks',
      icon: <OrderedListOutlined />,
      label: 'Tasks',
    },
    // Admin section (conditionally visible)
    ...(isAdmin
      ? [
          {
            type: 'divider' as const,
            style: { margin: '12px 0 8px' },
          },
          {
            key: 'admin-header',
            type: 'group' as const,
            label: (
              <span
                style={{
                  fontWeight: 500,
                  color: 'var(--color-text-primary)',
                  padding: '0 8px',
                }}
              >
                Admin
              </span>
            ),
          },
          {
            key: '/admin/users',
            icon: <UserOutlined />,
            label: 'Users',
          },
          {
            key: '/admin/containers',
            icon: <ContainerOutlined />,
            label: 'Containers',
          },
          {
            key: '/admin/datasets',
            icon: <DatabaseOutlined />,
            label: 'Datasets',
          },
          {
            key: '/admin/system',
            icon: <SettingOutlined />,
            label: 'System',
          },
        ]
      : []),
  ];

  // Determine selected key based on current path
  const getSelectedKey = () => {
    const path = location.pathname;
    if (projectId) {
      // Exact match for project sub-pages
      if (path === `/projects/${projectId}`) return `/projects/${projectId}`;
      if (path.startsWith(`/projects/${projectId}/datapacks`))
        return `/projects/${projectId}/datapacks`;
      if (path.startsWith(`/projects/${projectId}/executions`))
        return `/projects/${projectId}/executions`;
      if (path.startsWith(`/projects/${projectId}/evaluations`))
        return `/projects/${projectId}/evaluations`;
      if (path.startsWith(`/projects/${projectId}/settings`))
        return `/projects/${projectId}/settings`;
      // inject/execute/upload are under the project but map to Overview
      return `/projects/${projectId}`;
    }
    return path;
  };

  const handleMenuClick = ({ key }: { key: string }) => {
    if (key.startsWith('/')) {
      navigate(key);
      onNavigate?.();
    }
  };

  return (
    <div className='main-sidebar-content'>
      <Menu
        mode='inline'
        selectedKeys={[getSelectedKey()]}
        items={menuItems}
        onClick={handleMenuClick}
        className='main-sidebar-menu'
      />
      <div className='main-sidebar-footer'>
        <div className='system-status'>
          <div className='status-indicator' />
          <span className='status-text'>System Online</span>
        </div>
      </div>
    </div>
  );
};

export default MainSidebarContent;
