import { useState } from 'react';
import { Link, useLocation, useNavigate } from 'react-router-dom';

import {
  DashboardOutlined,
  LogoutOutlined,
  MenuFoldOutlined,
  MenuOutlined,
  MenuUnfoldOutlined,
  RightOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import {
  Avatar,
  Breadcrumb,
  Button,
  Drawer,
  Dropdown,
  type MenuProps,
  Space,
  Typography,
} from 'antd';

import { projectApi } from '@/api/projects';
import ThemeToggle from '@/components/ui/ThemeToggle';
import { useAuthStore } from '@/store/auth';

import MainSidebarContent from './MainSidebarContent';

import './AppHeader.css';

const { Text } = Typography;

interface BreadcrumbItem {
  label: string;
  path?: string;
}

interface AppHeaderProps {
  breadcrumbs?: BreadcrumbItem[];
  /** If true, uses a fixed sidebar toggle instead of drawer */
  sidebarMode?: 'drawer' | 'toggle';
  /** Sidebar collapsed state (only for toggle mode) */
  sidebarCollapsed?: boolean;
  /** Toggle sidebar callback (only for toggle mode) */
  onToggleSidebar?: () => void;
}

/**
 * Unified header component for all layouts
 * Contains hamburger menu, logo, breadcrumb, and user actions
 */
const AppHeader: React.FC<AppHeaderProps> = ({
  breadcrumbs,
  sidebarMode = 'drawer',
  sidebarCollapsed = false,
  onToggleSidebar,
}) => {
  const navigate = useNavigate();
  const location = useLocation();
  const { user, logout } = useAuthStore();
  const [drawerOpen, setDrawerOpen] = useState(false);

  // Resolve project ID from path for breadcrumb name resolution
  const pathParts = location.pathname.split('/').filter(Boolean);
  const projectIndex = pathParts.indexOf('projects');
  const projectIdStr =
    projectIndex >= 0 ? pathParts[projectIndex + 1] : undefined;
  const projectIdNum =
    projectIdStr && /^\d+$/.test(projectIdStr)
      ? Number(projectIdStr)
      : undefined;

  const { data: projectData } = useQuery({
    queryKey: ['project', projectIdNum],
    queryFn: () => projectApi.getProjectDetail(projectIdNum as number),
    enabled: !!projectIdNum,
  });

  // Check if user has admin privileges
  const isAdmin = !!user?.is_superuser;

  // User dropdown menu
  const userDropdownItems: MenuProps['items'] = [
    {
      key: 'profile',
      icon: <UserOutlined />,
      label: 'Profile',
    },
    // Admin Panel - only visible to superusers
    ...(isAdmin
      ? [
          {
            type: 'divider' as const,
          },
          {
            key: 'admin',
            icon: <DashboardOutlined />,
            label: 'Admin Panel',
          },
        ]
      : []),
    {
      type: 'divider',
    },
    {
      key: 'logout',
      icon: <LogoutOutlined />,
      label: 'Sign Out',
      danger: true,
    },
  ];

  const handleUserMenuClick = async ({ key }: { key: string }) => {
    if (key === 'logout') {
      await logout();
      navigate('/login');
    } else if (key === 'profile') {
      navigate('/profile');
    } else if (key === 'admin') {
      navigate('/admin/system');
    }
  };

  const handleMenuButtonClick = () => {
    if (sidebarMode === 'toggle' && onToggleSidebar) {
      onToggleSidebar();
    } else {
      setDrawerOpen((prev) => !prev);
    }
  };

  // Get menu icon based on mode
  const getMenuIcon = () => {
    if (sidebarMode === 'toggle') {
      return sidebarCollapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />;
    }
    // Drawer mode: show fold icon when open, menu icon when closed
    return drawerOpen ? <MenuFoldOutlined /> : <MenuOutlined />;
  };

  // Generate breadcrumb items from path if not provided
  const getBreadcrumbItems = () => {
    if (breadcrumbs) {
      // When custom breadcrumbs are provided, don't add Home icon
      return breadcrumbs.map((item, index) => ({
        title:
          item.path && index < breadcrumbs.length - 1 ? (
            <Link to={item.path}>{item.label}</Link>
          ) : (
            <span>{item.label}</span>
          ),
      }));
    }

    // Auto-generate from path
    const pathParts = location.pathname.split('/').filter(Boolean);

    // Special handling for profile page: show username instead of "Profile"
    if (pathParts[0] === 'profile' && user) {
      return [
        {
          title: <span>{user.username}</span>,
        },
      ];
    }

    // Special handling for team pages at root level: check if first segment is a team name
    // Team pages don't have a prefix, so we check if it's not a known route
    const knownRootRoutes = [
      'home',
      'projects',
      'profile',
      'admin',
      'settings',
      'tasks',
    ];
    if (
      pathParts.length >= 1 &&
      !knownRootRoutes.includes(pathParts[0]) &&
      !pathParts[0].match(/^\d+$/)
    ) {
      // This is likely a team or project page, preserve original case
      return [
        {
          title: <span>{pathParts[0]}</span>,
        },
      ];
    }

    const items: Array<{ title: React.ReactNode }> = [];

    let currentPath = '';
    pathParts.forEach((part, index) => {
      currentPath += `/${part}`;
      if (part === 'home') return;

      const isLast = index === pathParts.length - 1;

      // Resolve numeric IDs following 'projects/' to project name
      const prevPart = index > 0 ? pathParts[index - 1] : undefined;
      const isProjectId =
        prevPart === 'projects' && /^\d+$/.test(part);
      const label = isProjectId
        ? (projectData?.name ?? part)
        : part.charAt(0).toUpperCase() + part.slice(1).replace(/-/g, ' ');

      items.push({
        title: isLast ? (
          <span>{label}</span>
        ) : (
          <Link to={currentPath}>{label}</Link>
        ),
      });
    });

    return items;
  };

  return (
    <>
      <header className='app-header'>
        <div className='app-header-left'>
          {/* Hamburger menu / sidebar toggle */}
          <Button
            type='text'
            icon={getMenuIcon()}
            onClick={handleMenuButtonClick}
            className='app-header-menu-btn'
            aria-label={
              sidebarMode === 'toggle'
                ? sidebarCollapsed
                  ? 'Expand sidebar'
                  : 'Collapse sidebar'
                : drawerOpen
                  ? 'Close navigation menu'
                  : 'Open navigation menu'
            }
          />

          {/* Logo */}
          <div className='app-header-logo' onClick={() => navigate('/home')}>
            <div className='app-header-logo-icon'>
              <svg
                width='28'
                height='28'
                viewBox='0 0 32 32'
                fill='none'
                role='img'
                aria-label='AegisLab logo'
              >
                <title>AegisLab</title>
                <path
                  d='M16 2L30 8.5V23.5L16 30L2 23.5V8.5L16 2Z'
                  stroke='currentColor'
                  strokeWidth='2'
                  strokeLinejoin='round'
                />
                <path
                  d='M16 16L30 8.5M16 16V30M16 16L2 8.5'
                  stroke='currentColor'
                  strokeWidth='2'
                  strokeLinejoin='round'
                  opacity='0.3'
                />
                <circle cx='16' cy='16' r='4' fill='currentColor' />
              </svg>
            </div>
            <span className='app-header-logo-text'>AegisLab</span>
          </div>

          {/* Breadcrumb */}
          <Breadcrumb
            className='app-header-breadcrumb'
            separator={<RightOutlined style={{ fontSize: '10px' }} />}
            items={getBreadcrumbItems()}
          />
        </div>

        <div className='app-header-right'>
          <ThemeToggle />
          <Dropdown
            menu={{ items: userDropdownItems, onClick: handleUserMenuClick }}
            placement='bottomRight'
            arrow
          >
            <Space
              className='app-header-user'
              role='button'
              tabIndex={0}
              aria-label='User menu'
            >
              <Avatar
                size='small'
                icon={<UserOutlined />}
                style={{ backgroundColor: 'var(--color-primary-500)' }}
              />
              <Text className='app-header-username'>
                {user?.username || 'User'}
              </Text>
            </Space>
          </Dropdown>
        </div>
      </header>

      {/* Main sidebar drawer (only for drawer mode) */}
      {sidebarMode === 'drawer' && (
        <Drawer
          title={
            <div className='app-drawer-title'>
              <div className='app-drawer-logo'>
                <svg
                  width='24'
                  height='24'
                  viewBox='0 0 32 32'
                  fill='none'
                  role='img'
                >
                  <path
                    d='M16 2L30 8.5V23.5L16 30L2 23.5V8.5L16 2Z'
                    stroke='currentColor'
                    strokeWidth='2'
                    strokeLinejoin='round'
                  />
                  <circle cx='16' cy='16' r='4' fill='currentColor' />
                </svg>
              </div>
              <span>AegisLab</span>
            </div>
          }
          placement='left'
          onClose={() => setDrawerOpen(false)}
          open={drawerOpen}
          width={280}
          className='app-sidebar-drawer'
        >
          <MainSidebarContent onNavigate={() => setDrawerOpen(false)} />
        </Drawer>
      )}
    </>
  );
};

export default AppHeader;
