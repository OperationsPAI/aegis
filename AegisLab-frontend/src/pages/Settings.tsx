import { NavLink, useLocation } from 'react-router-dom';

import {
  Chip,
  PageHeader,
  Panel,
  SettingsSection,
  FormRow,
  DangerZone,
  MonoValue,
} from '@/components/ui';
import './Settings.css';

interface SettingsNavItem {
  key: string;
  label: string;
}

const SETTINGS_NAV: SettingsNavItem[] = [
  { key: 'users', label: 'Users' },
  { key: 'teams', label: 'Teams' },
  { key: 'roles', label: 'Roles & Permissions' },
  { key: 'api-keys', label: 'API Keys' },
  { key: 'audit', label: 'Audit Logs' },
];

/* ── Demo form components (inline until we build real ones) ─────── */

function DemoInput({ value, placeholder }: { value?: string; placeholder?: string }) {
  return (
    <input
      type="text"
      defaultValue={value}
      placeholder={placeholder}
      className="settings-demo-input"
    />
  );
}

function DemoToggle({ checked }: { checked?: boolean }) {
  return (
    <label className="settings-demo-toggle">
      <input type="checkbox" defaultChecked={checked} />
      <span className="settings-demo-toggle__track" />
    </label>
  );
}

/* ── Content per tab ─────────────────────────────────────────────── */

function UsersContent() {
  return (
    <>
      <PageHeader
        title="Users"
        description="Manage team members and their access to projects and resources."
        action={<Chip tone="ink">+ Invite user</Chip>}
      />
      <SettingsSection
        title="General"
        description="Basic user information and preferences."
      >
        <FormRow label="Display name" description="Shown across the platform.">
          <DemoInput value="Ada Lovelace" />
        </FormRow>
        <FormRow label="Email" description="Used for notifications and login.">
          <DemoInput value="ada@aegislab.io" />
        </FormRow>
        <FormRow label="Role" description="Determines permissions across the workspace.">
          <DemoInput value="Admin" />
        </FormRow>
      </SettingsSection>

      <SettingsSection
        title="Notifications"
        description="Choose what you want to be notified about."
      >
        <FormRow label="Email alerts" description="Receive email for critical events.">
          <DemoToggle checked />
        </FormRow>
        <FormRow label="Slack integration" description="Push notifications to your Slack channel.">
          <DemoToggle />
        </FormRow>
      </SettingsSection>

      <DangerZone
        description="Once you delete a user, all their associated data will be permanently removed. This action cannot be undone."
      >
        <div className="settings-demo-danger-row">
          <span>Delete this user account</span>
          <button type="button" className="settings-demo-danger-btn">Delete user</button>
        </div>
      </DangerZone>
    </>
  );
}

function TeamsContent() {
  return (
    <>
      <PageHeader
        title="Teams"
        description="Organize members into teams for easier project access control."
        action={<Chip tone="ink">+ Create team</Chip>}
      />
      <SettingsSection
        title="Your teams"
        description="Teams you are a member of."
      >
        <FormRow label="Platform Team" description="12 members · 3 projects">
          <MonoValue size="sm">platform-team</MonoValue>
        </FormRow>
        <FormRow label="SRE Team" description="5 members · 8 projects">
          <MonoValue size="sm">sre-team</MonoValue>
        </FormRow>
      </SettingsSection>
    </>
  );
}

function RolesContent() {
  return (
    <>
      <PageHeader
        title="Roles & Permissions"
        description="Define roles and configure access control rules for your workspace."
      />
      <SettingsSection
        title="System roles"
        description="Built-in roles that cannot be deleted."
      >
        <FormRow label="Admin" description="Full access to all resources and settings.">
          <MonoValue size="sm">all-permissions</MonoValue>
        </FormRow>
        <FormRow label="Editor" description="Can create and modify experiments, cannot manage users.">
          <MonoValue size="sm">read-write</MonoValue>
        </FormRow>
        <FormRow label="Viewer" description="Read-only access to projects and results.">
          <MonoValue size="sm">read-only</MonoValue>
        </FormRow>
      </SettingsSection>
    </>
  );
}

function ApiKeysContent() {
  return (
    <>
      <PageHeader
        title="API Keys"
        description="Manage SDK and service API keys for programmatic access."
        action={<Chip tone="ink">+ Generate key</Chip>}
      />
      <SettingsSection
        title="Active keys"
        description="Keys with access to the API."
      >
        <FormRow label="Production SDK" description="Created 2024-03-15 · Last used 2 h ago">
          <MonoValue size="sm">ak_live_••••••••••••8f2a</MonoValue>
        </FormRow>
        <FormRow label="CI/CD Pipeline" description="Created 2024-01-20 · Last used 1 d ago">
          <MonoValue size="sm">ak_test_••••••••••••3b71</MonoValue>
        </FormRow>
      </SettingsSection>
    </>
  );
}

function AuditContent() {
  return (
    <>
      <PageHeader
        title="Audit Logs"
        description="Review system activity and changes across your workspace."
      />
      <SettingsSection
        title="Recent events"
        description="Last 30 days of activity."
      >
        <FormRow label="user.created" description="2024-05-10 14:32:01 · admin@aegislab.io">
          <MonoValue size="sm">user:grace-hopper</MonoValue>
        </FormRow>
        <FormRow label="project.updated" description="2024-05-10 11:15:33 · ada@aegislab.io">
          <MonoValue size="sm">project:catalog-service</MonoValue>
        </FormRow>
        <FormRow label="injection.executed" description="2024-05-09 09:42:18 · service-account">
          <MonoValue size="sm">injection:clock-drift-01</MonoValue>
        </FormRow>
      </SettingsSection>
    </>
  );
}

const CONTENT_MAP: Record<string, React.FC> = {
  users: UsersContent,
  teams: TeamsContent,
  roles: RolesContent,
  'api-keys': ApiKeysContent,
  audit: AuditContent,
};

/* ── Main page ───────────────────────────────────────────────────── */

export default function Settings() {
  const location = useLocation();

  const activeKey =
    SETTINGS_NAV.find((t) => location.pathname.endsWith(t.key))?.key ??
    'users';

  const Content = CONTENT_MAP[activeKey] ?? UsersContent;

  return (
    <div className="page-wrapper settings-page">
      <aside className="settings-page__sidebar">
        <div className="settings-page__sidebar-header">
          <span className="settings-page__sidebar-title">Settings</span>
        </div>
        <nav className="settings-page__nav">
          {SETTINGS_NAV.map((item) => (
            <NavLink
              key={item.key}
              to={`/settings/${item.key}`}
              className={({ isActive }) =>
                isActive
                  ? 'settings-page__nav-link settings-page__nav-link--active'
                  : 'settings-page__nav-link'
              }
            >
              <span className="settings-page__nav-label">{item.label}</span>
            </NavLink>
          ))}
        </nav>
      </aside>

      <main className="settings-page__main">
        <Panel className="settings-page__panel">
          <Content />
        </Panel>
      </main>
    </div>
  );
}
