import { useCallback, useMemo, useState } from 'react';

import {
  DeleteOutlined,
  PlusOutlined,
  ReloadOutlined,
  SearchOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Badge,
  Button,
  Descriptions,
  Input,
  Modal,
  Popconfirm,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import dayjs from 'dayjs';

import { usersApi } from '@/api/users';
import { createdAtColumn } from '@/components/ui/columns/createdAtColumn';
import { usePagination } from '@/hooks/usePagination';

import AssignRoleDrawer from './components/AssignRoleDrawer';
import { useUserMutations } from './hooks/useUserMutations';

import type { RoleRecord, UserRecord } from './types';

const { Text } = Typography;

const UsersTab: React.FC = () => {
  const queryClient = useQueryClient();
  const {
    current: page,
    pageSize,
    onChange: onPageChange,
    reset: resetPage,
  } = usePagination();
  const [searchText, setSearchText] = useState('');
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [selectedUser, setSelectedUser] = useState<UserRecord | null>(null);

  const { data: usersData, isLoading } = useQuery({
    queryKey: ['admin-users', page, pageSize, searchText],
    queryFn: () =>
      usersApi.getUsers({
        page,
        size: pageSize,
        username: searchText || undefined,
      }),
    staleTime: 10_000,
  });

  const { assignRoleMutation, removeRoleMutation, deleteUserMutation } =
    useUserMutations();

  const users: UserRecord[] = useMemo(() => {
    if (!usersData?.items) return [];
    // Backend enriches UserResp with roles; UserRecord extends UserResp
    return usersData.items as UserRecord[];
  }, [usersData]);

  const total = useMemo(() => {
    if (!usersData) return 0;
    return usersData.pagination?.total ?? users.length;
  }, [usersData, users.length]);

  const openAssignDrawer = useCallback((user: UserRecord) => {
    setSelectedUser(user);
    setDrawerOpen(true);
  }, []);

  const handleAssignRole = useCallback(
    (userId: number, roleId: number) => {
      assignRoleMutation.mutate(
        { userId, roleId },
        { onSuccess: () => setDrawerOpen(false) }
      );
    },
    [assignRoleMutation]
  );

  const columns: ColumnsType<UserRecord> = [
    {
      title: 'Username',
      dataIndex: 'username',
      key: 'username',
      width: 180,
      render: (text: string) => (
        <Space>
          <UserOutlined />
          <Text strong style={{ color: 'var(--color-primary-600)' }}>
            {text}
          </Text>
        </Space>
      ),
    },
    {
      title: 'Email',
      dataIndex: 'email',
      key: 'email',
      width: 240,
      ellipsis: true,
    },
    {
      title: 'Status',
      dataIndex: 'is_active',
      key: 'is_active',
      width: 100,
      render: (active: boolean) => (
        <Badge
          status={active ? 'success' : 'error'}
          text={active ? 'Active' : 'Inactive'}
        />
      ),
    },
    {
      title: 'Roles',
      dataIndex: 'roles',
      key: 'roles',
      width: 280,
      render: (roles: RoleRecord[] | undefined, record: UserRecord) => {
        if (!roles || roles.length === 0) {
          return <Text type='secondary'>No roles</Text>;
        }
        return (
          <Space size={4} wrap>
            {roles.map((role) => (
              <Tag
                key={role.id}
                color='blue'
                closable
                onClose={(e) => {
                  e.preventDefault();
                  Modal.confirm({
                    title: 'Remove Role',
                    content: `Remove role "${role.name}" from user "${record.username}"?`,
                    okText: 'Remove',
                    okButtonProps: { danger: true },
                    onOk: () =>
                      removeRoleMutation.mutate({
                        userId: record.id,
                        roleId: role.id,
                      }),
                  });
                }}
              >
                {role.name}
              </Tag>
            ))}
          </Space>
        );
      },
    },
    createdAtColumn<UserRecord>(),
    {
      title: 'Actions',
      key: 'actions',
      width: 160,
      render: (_: unknown, record: UserRecord) => (
        <Space>
          <Tooltip title='Assign Role'>
            <Button
              type='text'
              size='small'
              icon={<PlusOutlined />}
              onClick={() => openAssignDrawer(record)}
            />
          </Tooltip>
          <Popconfirm
            title='Delete this user?'
            description='This action cannot be undone.'
            okText='Delete'
            okButtonProps={{ danger: true }}
            onConfirm={() => deleteUserMutation.mutate(record.id)}
          >
            <Button type='text' size='small' danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  const expandedRowRender = (record: UserRecord) => (
    <Descriptions size='small' column={3} bordered>
      <Descriptions.Item label='User ID'>{record.id}</Descriptions.Item>
      <Descriptions.Item label='Email'>{record.email}</Descriptions.Item>
      <Descriptions.Item label='Status'>
        <Badge
          status={record.is_active ? 'success' : 'error'}
          text={record.is_active ? 'Active' : 'Inactive'}
        />
      </Descriptions.Item>
      <Descriptions.Item label='Roles' span={3}>
        {record.roles && record.roles.length > 0 ? (
          <Space wrap>
            {record.roles.map((role) => (
              <Tag key={role.id} color='blue'>
                {role.name}
                {role.scope && (
                  <Text
                    type='secondary'
                    style={{ fontSize: 11, marginLeft: 4 }}
                  >
                    ({role.scope})
                  </Text>
                )}
              </Tag>
            ))}
          </Space>
        ) : (
          <Text type='secondary'>No roles assigned</Text>
        )}
      </Descriptions.Item>
      {record.created_at && (
        <Descriptions.Item label='Created'>
          {dayjs(record.created_at).format('YYYY-MM-DD HH:mm:ss')}
        </Descriptions.Item>
      )}
      {record.updated_at && (
        <Descriptions.Item label='Updated'>
          {dayjs(record.updated_at).format('YYYY-MM-DD HH:mm:ss')}
        </Descriptions.Item>
      )}
    </Descriptions>
  );

  return (
    <>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          marginBottom: 16,
        }}
      >
        <Input
          placeholder='Search by username...'
          prefix={<SearchOutlined />}
          allowClear
          value={searchText}
          onChange={(e) => {
            setSearchText(e.target.value);
            resetPage();
          }}
          style={{ width: 300 }}
        />
        <Button
          icon={<ReloadOutlined />}
          onClick={() =>
            queryClient.invalidateQueries({ queryKey: ['admin-users'] })
          }
        >
          Refresh
        </Button>
      </div>

      <Table<UserRecord>
        rowKey='id'
        columns={columns}
        dataSource={users}
        loading={isLoading}
        expandable={{
          expandedRowRender,
          rowExpandable: () => true,
        }}
        pagination={{
          current: page,
          pageSize,
          total,
          showSizeChanger: true,
          showTotal: (t) => `Total ${t} users`,
          onChange: onPageChange,
        }}
        size='middle'
      />

      <AssignRoleDrawer
        open={drawerOpen}
        selectedUser={selectedUser}
        onClose={() => setDrawerOpen(false)}
        onAssign={handleAssignRole}
        loading={assignRoleMutation.isPending}
      />
    </>
  );
};

export default UsersTab;
