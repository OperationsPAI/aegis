import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import {
  AppstoreOutlined,
  BellOutlined,
  BranchesOutlined,
  DashboardOutlined,
  DownOutlined,
  ExperimentOutlined,
  EyeOutlined,
  HddOutlined,
  LineChartOutlined,
  LogoutOutlined,
  PlayCircleOutlined,
  ProfileOutlined,
  SearchOutlined,
  SettingOutlined,
  TagsOutlined,
  UserOutlined,
} from '@ant-design/icons';
import {
  NavLink,
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from 'react-router-dom';

import {
  Avatar,
  Breadcrumb,
  type BreadcrumbItem,
  DropdownMenu,
  type DropdownItem,
  ProjectSelector,
  type ProjectOption,
} from '@/components/ui';
import { projectsApi, type ProjectProjectResp } from '@/api/portal-client';
import Dashboard from './pages/Dashboard';
import Gallery from './pages/Gallery';
import Settings from './pages/Settings';
import Projects from './pages/Projects';
import ProjectOverview from './pages/ProjectOverview';
import Injections from './pages/Injections';
import InjectionDetail from './pages/InjectionDetail';
import Executions from './pages/Executions';
import ExecutionDetail from './pages/ExecutionDetail';
import Traces from './pages/Traces';
import TraceDetail from './pages/TraceDetail';
import Observations from './pages/Observations';
import MetricsPage from './pages/MetricsPage';
import Containers from './pages/Containers';
import ContainerDetail from './pages/ContainerDetail';
import Datasets from './pages/Datasets';
import DatasetDetail from './pages/DatasetDetail';
import Labels from './pages/Labels';
import LabelDetail from './pages/LabelDetail';
import Tasks from './pages/Tasks';
import TaskDetail from './pages/TaskDetail';
import ProjectCreate from './pages/ProjectCreate';
import InjectionCreate from './pages/InjectionCreate';
import ExecutionCreate from './pages/ExecutionCreate';
import ContainerCreate from './pages/ContainerCreate';
import DatasetCreate from './pages/DatasetCreate';
import LabelCreate from './pages/LabelCreate';
import './App.css';
import './pages/pages.css';

/* ── Navigation definitions ──────────────────────────────────────── */

interface NavItem {
  to: string;
  label: string;
  icon: ReactNode;
  end?: boolean;
}

const GLOBAL_NAV: NavItem[] = [
  { to: '/', label: 'Dashboard', icon: <DashboardOutlined />, end: true },
  { to: '/gallery', label: 'Gallery', icon: <AppstoreOutlined /> },
];

const RESOURCE_NAV: NavItem[] = [
  { to: '/containers', label: 'Containers', icon: <HddOutlined /> },
  { to: '/datasets', label: 'Datasets', icon: <ProfileOutlined /> },
  { to: '/labels', label: 'Labels', icon: <TagsOutlined /> },
  { to: '/tasks', label: 'Tasks', icon: <PlayCircleOutlined /> },
];

const PROJECT_NAV: NavItem[] = [
  { to: 'overview', label: 'Overview', icon: <DashboardOutlined /> },
  { to: 'injections', label: 'Injections', icon: <ExperimentOutlined /> },
  { to: 'executions', label: 'Executions', icon: <PlayCircleOutlined /> },
  { to: 'traces', label: 'Traces', icon: <BranchesOutlined /> },
  { to: 'observations', label: 'Observations', icon: <EyeOutlined /> },
  { to: 'metrics', label: 'Metrics', icon: <LineChartOutlined /> },
];

const ADMIN_NAV: NavItem[] = [
  { to: '/settings', label: 'Settings', icon: <SettingOutlined /> },
];

/* ── Toggle button ───────────────────────────────────────────────── */

function ToggleButton({
  collapsed,
  onClick,
}: {
  collapsed: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className="app-sidebar__toggle"
      onClick={onClick}
      aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
      aria-expanded={!collapsed}
    >
      <span className="app-sidebar__toggle-bar" />
      <span className="app-sidebar__toggle-bar" />
      <span className="app-sidebar__toggle-bar" />
    </button>
  );
}

/* ── Sidebar ─────────────────────────────────────────────────────── */

function Sidebar({
  collapsed,
  onToggle,
  projectId,
}: {
  collapsed: boolean;
  onToggle: () => void;
  projectId?: string;
}) {
  const location = useLocation();

  return (
    <aside
      className={`app-sidebar ${collapsed ? 'app-sidebar--collapsed' : ''}`}
    >
      <div className="app-sidebar__brand">
        <span className="app-sidebar__logo">
          <span className="app-sidebar__logo-full">AegisLab</span>
          <span className="app-sidebar__logo-mini">A</span>
        </span>
        <ToggleButton collapsed={collapsed} onClick={onToggle} />
      </div>

      <nav className="app-sidebar__nav">
        {/* Flat nav — all items at the same level */}
        <div className="app-sidebar__section">
          {GLOBAL_NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) =>
                isActive
                  ? 'app-sidebar__link app-sidebar__link--active'
                  : 'app-sidebar__link'
              }
              title={item.label}
            >
              <span className="app-sidebar__link-icon">{item.icon}</span>
              <span className="app-sidebar__link-text">{item.label}</span>
            </NavLink>
          ))}
          {projectId &&
            PROJECT_NAV.map((item) => (
              <NavLink
                key={item.to}
                to={`/projects/${projectId}/${item.to}`}
                className={({ isActive }) =>
                  isActive
                    ? 'app-sidebar__link app-sidebar__link--active'
                    : 'app-sidebar__link'
                }
                title={item.label}
              >
                <span className="app-sidebar__link-icon">{item.icon}</span>
                <span className="app-sidebar__link-text">{item.label}</span>
              </NavLink>
            ))}
          {RESOURCE_NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) =>
                isActive
                  ? 'app-sidebar__link app-sidebar__link--active'
                  : 'app-sidebar__link'
              }
              title={item.label}
            >
              <span className="app-sidebar__link-icon">{item.icon}</span>
              <span className="app-sidebar__link-text">{item.label}</span>
            </NavLink>
          ))}
        </div>

        <div className="app-sidebar__spacer" />

        <div className="app-sidebar__section">
          {ADMIN_NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                isActive
                  ? 'app-sidebar__link app-sidebar__link--active'
                  : 'app-sidebar__link'
              }
              title={item.label}
            >
              <span className="app-sidebar__link-icon">{item.icon}</span>
              <span className="app-sidebar__link-text">{item.label}</span>
            </NavLink>
          ))}
        </div>
      </nav>

      {/* Collapsed hint */}
      {collapsed && (
        <div className="app-sidebar__current-route">
          {[
            ...GLOBAL_NAV,
            ...RESOURCE_NAV,
            ...PROJECT_NAV,
            ...ADMIN_NAV,
          ].find(
            (item) =>
              item.to === location.pathname ||
              (item.to !== '/' && location.pathname.startsWith(item.to)),
          )?.label ?? 'AegisLab'}
        </div>
      )}
    </aside>
  );
}

/* ── Top header ──────────────────────────────────────────────────── */

function TopHeader({
  projectId,
  projects,
  onProjectChange,
}: {
  projectId?: string;
  projects: ProjectOption[];
  onProjectChange: (id: string) => void;
}) {
  const location = useLocation();
  const navigate = useNavigate();

  const breadcrumbItems: BreadcrumbItem[] = useMemo(() => {
    const path = location.pathname;
    const items: BreadcrumbItem[] = [];

    // Dashboard
    if (path === '/') {
      items.push({ label: 'Dashboard' });
      return items;
    }

    // Gallery
    if (path === '/gallery') {
      items.push({ label: 'Gallery' });
      return items;
    }

    // Settings
    if (path.startsWith('/settings')) {
      items.push({ label: 'Settings', to: '/settings' });
      const tab = path.split('/').pop() ?? 'users';
      const tabLabel =
        {
          users: 'Users',
          teams: 'Teams',
          roles: 'Roles & Permissions',
          'api-keys': 'API Keys',
          audit: 'Audit Logs',
        }[tab] ?? tab;
      items.push({ label: tabLabel });
      return items;
    }

    // Resources (list + detail)
    const resourceMatch = [...RESOURCE_NAV, ...ADMIN_NAV].find(
      (item) => item.to === path || (item.to !== '/' && path.startsWith(item.to)),
    );
    if (resourceMatch) {
      items.push({ label: resourceMatch.label, to: resourceMatch.to });
      const detailId = path.split('/').pop();
      if (detailId && detailId !== resourceMatch.to.replace('/', '')) {
        items.push({ label: detailId });
      }
      return items;
    }

    // Projects list
    if (path === '/projects') {
      items.push({ label: 'Projects' });
      return items;
    }

    // New project
    if (path === '/projects/new') {
      items.push({ label: 'Projects', to: '/projects' });
      items.push({ label: 'New Project' });
      return items;
    }

    // Project-scoped
    if (path.startsWith('/projects/')) {
      const parts = path.split('/').filter(Boolean);
      const pid = parts[1];
      const section = parts[2];
      const detailId = parts[3];

      items.push({ label: 'Projects', to: '/projects' });

      const project = projects.find((p) => p.id === pid);
      items.push({
        label: project?.name ?? pid,
        to: `/projects/${pid}`,
      });

      const nav = section ? PROJECT_NAV.find((n) => n.to === section) : undefined;
      if (section) {
        items.push({
          label: nav?.label ?? section,
          to: detailId ? `/projects/${pid}/${section}` : undefined,
        });
      }

      if (detailId) {
        const label =
          detailId === 'new'
            ? `New ${nav?.label ?? section}`
            : detailId;
        items.push({ label });
      }
      return items;
    }

    items.push({ label: 'AegisLab' });
    return items;
  }, [location.pathname, projects]);

  const userMenuItems: DropdownItem[] = [
    {
      key: 'profile',
      label: 'Profile',
      icon: <UserOutlined />,
      onClick: () => navigate('/settings/users'),
    },
    {
      key: 'settings',
      label: 'Settings',
      icon: <SettingOutlined />,
      onClick: () => navigate('/settings'),
    },
    {
      key: 'divider',
      label: '',
      disabled: true,
    },
    {
      key: 'logout',
      label: 'Logout',
      icon: <LogoutOutlined />,
      danger: true,
      onClick: () => {
        // TODO: wire to auth store logout
        window.location.reload();
      },
    },
  ];

  return (
    <header className="app-top-header">
      <div className="app-top-header__left">
        <Breadcrumb items={breadcrumbItems} />
      </div>
      <div className="app-top-header__right">
        {projects.length > 0 && (
          <ProjectSelector
            projects={projects}
            selectedId={projectId}
            onSelect={onProjectChange}
            placeholder="Select project…"
            align="right"
          />
        )}
        <button
          type="button"
          className="app-top-header__icon-btn"
          aria-label="Search"
        >
          <SearchOutlined />
        </button>
        <button
          type="button"
          className="app-top-header__icon-btn"
          aria-label="Notifications"
        >
          <BellOutlined />
        </button>
        <DropdownMenu
          trigger={
            <div className="app-top-header__user">
              <Avatar size="sm" name="User" />
              <span className="app-top-header__user-name">User</span>
              <DownOutlined style={{ fontSize: 10 }} />
            </div>
          }
          items={userMenuItems}
          align="right"
        />
      </div>
    </header>
  );
}

/* ── Project layout (routes under /projects/:projectId) ──────────── */

function ProjectLayout() {
  return (
    <Routes>
      <Route path="overview" element={<ProjectOverview />} />
      <Route path="injections" element={<Injections />} />
      <Route path="injections/new" element={<InjectionCreate />} />
      <Route path="injections/:injectionId" element={<InjectionDetail />} />
      <Route path="executions" element={<Executions />} />
      <Route path="executions/new" element={<ExecutionCreate />} />
      <Route path="executions/:executionId" element={<ExecutionDetail />} />
      <Route path="traces" element={<Traces />} />
      <Route path="traces/:traceId" element={<TraceDetail />} />
      <Route path="observations" element={<Observations />} />
      <Route path="metrics" element={<MetricsPage />} />
      <Route path="*" element={<Navigate to="overview" replace />} />
    </Routes>
  );
}

/* ── Main App ────────────────────────────────────────────────────── */

export default function App() {
  const [collapsed, setCollapsed] = useState(false);
  const toggle = useCallback(() => setCollapsed((c) => !c), []);

  const [projects, setProjects] = useState<ProjectProjectResp[]>([]);
  const [projectId, setProjectId] = useState<string | undefined>();

  const navigate = useNavigate();
  const location = useLocation();

  // Extract projectId from URL — once set, it persists across navigation
  useEffect(() => {
    const match = location.pathname.match(/^\/projects\/([^/]+)/);
    if (match && match[1] !== 'new') {
      setProjectId(match[1]);
    }
  }, [location.pathname]);

  // Load project list
  useEffect(() => {
    let cancelled = false;
    projectsApi
      .listProjects({ page: 1, size: 100 })
      .then((res) => {
        if (cancelled) return;
        setProjects(res.data.data?.items ?? []);
      })
      .catch(() => {
        if (cancelled) return;
        setProjects([
          { id: 1, name: 'demo-alpha' },
          { id: 2, name: 'demo-beta' },
          { id: 3, name: 'demo-gamma' },
        ] as ProjectProjectResp[]);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const projectOptions: ProjectOption[] = useMemo(() => {
    const items =
      projects.length > 0
        ? projects
        : ([
            { id: 1, name: 'demo-alpha' },
            { id: 2, name: 'demo-beta' },
            { id: 3, name: 'demo-gamma' },
          ] as ProjectProjectResp[]);
    return items.map((p) => ({
      id: String(p.id ?? p.name ?? 'unknown'),
      name: p.name ?? 'Untitled',
    }));
  }, [projects]);

  const handleProjectChange = useCallback(
    (id: string) => {
      navigate(`/projects/${id}/injections`);
    },
    [navigate],
  );

  return (
    <div className="app-shell">
      <Sidebar
        collapsed={collapsed}
        onToggle={toggle}
        projectId={projectId}
      />
      <div
        className={`app-content ${collapsed ? 'app-content--full' : ''}`}
      >
        <TopHeader
          projectId={projectId}
          projects={projectOptions}
          onProjectChange={handleProjectChange}
        />
        <main className="app-main">
          <Routes>
            <Route path="/" element={<Dashboard />} />
            <Route path="/projects" element={<Projects />} />
            <Route path="/projects/new" element={<ProjectCreate />} />
            <Route path="/projects/:projectId/*" element={<ProjectLayout />} />
            <Route path="/containers" element={<Containers />} />
            <Route path="/containers/new" element={<ContainerCreate />} />
            <Route path="/containers/:containerId" element={<ContainerDetail />} />
            <Route path="/datasets" element={<Datasets />} />
            <Route path="/datasets/new" element={<DatasetCreate />} />
            <Route path="/datasets/:datasetId" element={<DatasetDetail />} />
            <Route path="/labels" element={<Labels />} />
            <Route path="/labels/new" element={<LabelCreate />} />
            <Route path="/labels/:labelId" element={<LabelDetail />} />
            <Route path="/tasks" element={<Tasks />} />
            <Route path="/tasks/:taskId" element={<TaskDetail />} />
            <Route path="/gallery" element={<Gallery />} />
            <Route path="/settings/*" element={<Settings />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </main>
      </div>
    </div>
  );
}
