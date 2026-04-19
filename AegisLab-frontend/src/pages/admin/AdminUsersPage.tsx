/**
 * AdminUsersPage - RBAC user and role management.
 *
 * Route: /admin/users
 *
 * Two tabs:
 * - Users: List all users with role badges, assign/remove roles via drawer
 * - Roles: List all roles with permissions management, create/delete roles
 */
import { TeamOutlined, UserOutlined } from '@ant-design/icons';
import { Card, Tabs, Typography } from 'antd';

import RolesTab from './users/RolesTab';
import UsersTab from './users/UsersTab';

const { Title } = Typography;

const AdminUsersPage: React.FC = () => {
  return (
    <div style={{ padding: 24 }}>
      <Title level={4} style={{ marginBottom: 24 }}>
        <TeamOutlined style={{ marginRight: 8 }} />
        User & Role Management
      </Title>

      <Card>
        <Tabs
          defaultActiveKey='users'
          items={[
            {
              key: 'users',
              label: (
                <span>
                  <UserOutlined /> Users
                </span>
              ),
              children: <UsersTab />,
            },
            {
              key: 'roles',
              label: (
                <span>
                  <TeamOutlined /> Roles
                </span>
              ),
              children: <RolesTab />,
            },
          ]}
        />
      </Card>
    </div>
  );
};

export default AdminUsersPage;
