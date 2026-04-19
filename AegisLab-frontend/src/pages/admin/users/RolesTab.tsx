import { useMemo, useState } from 'react';

import {
  DeleteOutlined,
  PlusOutlined,
  ReloadOutlined,
  TeamOutlined,
} from '@ant-design/icons';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Badge,
  Button,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';

import { roleApi } from '@/api/roles';
import { createdAtColumn } from '@/components/ui/columns/createdAtColumn';
import { usePagination } from '@/hooks/usePagination';

import CreateRoleModal from './components/CreateRoleModal';
import { useRoleMutations } from './hooks/useRoleMutations';

import RolePermissionsView from './RolePermissionsView';
import type { RoleRecord } from './types';

const { Text } = Typography;

const RolesTab: React.FC = () => {
  const queryClient = useQueryClient();
  const {
    current: page,
    pageSize,
    onChange: onPageChange,
    reset: resetPage,
  } = usePagination();
  const [scopeFilter, setScopeFilter] = useState<string | undefined>();
  const [createModalOpen, setCreateModalOpen] = useState(false);

  const { data: rolesData, isLoading } = useQuery({
    queryKey: ['admin-roles', page, pageSize, scopeFilter],
    queryFn: () =>
      roleApi.getRoles({ page, size: pageSize, scope: scopeFilter }),
    staleTime: 10_000,
  });

  const { createRoleMutation, deleteRoleMutation } = useRoleMutations();

  // getRoles returns untyped data; normalise into RoleRecord[]
  const roles: RoleRecord[] = useMemo(() => {
    if (!rolesData) return [];
    if (Array.isArray(rolesData)) return rolesData as RoleRecord[];
    const data = rolesData as { items?: RoleRecord[] };
    return data.items ?? [];
  }, [rolesData]);

  const total = useMemo(() => {
    if (!rolesData) return 0;
    if (Array.isArray(rolesData)) return rolesData.length;
    const data = rolesData as { total?: number };
    return data.total ?? roles.length;
  }, [rolesData, roles.length]);

  const handleCreateRole = (values: {
    name: string;
    display_name: string;
    description?: string;
  }) => {
    createRoleMutation.mutate(values, {
      onSuccess: () => setCreateModalOpen(false),
    });
  };

  const columns: ColumnsType<RoleRecord> = [
    {
      title: 'Role Name',
      dataIndex: 'name',
      key: 'name',
      width: 200,
      render: (text: string) => (
        <Space>
          <TeamOutlined />
          <Text strong style={{ color: 'var(--color-primary-600)' }}>
            {text}
          </Text>
        </Space>
      ),
    },
    {
      title: 'Scope',
      dataIndex: 'scope',
      key: 'scope',
      width: 130,
      render: (scope?: string) => {
        if (!scope) return <Text type='secondary'>-</Text>;
        const colorMap: Record<string, string> = {
          global: 'purple',
          project: 'blue',
          container: 'cyan',
          dataset: 'green',
        };
        return (
          <Tag color={colorMap[scope.toLowerCase()] ?? 'default'}>{scope}</Tag>
        );
      },
    },
    {
      title: 'Description',
      dataIndex: 'description',
      key: 'description',
      ellipsis: true,
      render: (desc?: string) => <Text type='secondary'>{desc ?? '-'}</Text>,
    },
    {
      title: 'Permissions',
      dataIndex: 'permissions_count',
      key: 'permissions_count',
      width: 120,
      align: 'center',
      render: (count?: number) => (
        <Badge
          count={count ?? 0}
          showZero
          style={{
            backgroundColor: count
              ? 'var(--color-primary-600)'
              : 'var(--color-secondary-300)',
          }}
        />
      ),
    },
    createdAtColumn<RoleRecord>(),
    {
      title: 'Actions',
      key: 'actions',
      width: 100,
      render: (_: unknown, record: RoleRecord) => (
        <Popconfirm
          title='Delete this role?'
          description='This will remove the role from all assigned users.'
          okText='Delete'
          okButtonProps={{ danger: true }}
          onConfirm={() => deleteRoleMutation.mutate(record.id)}
        >
          <Button type='text' size='small' danger icon={<DeleteOutlined />} />
        </Popconfirm>
      ),
    },
  ];

  return (
    <>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          marginBottom: 16,
        }}
      >
        <Space>
          <Select
            placeholder='Filter by scope'
            allowClear
            value={scopeFilter}
            onChange={(val) => {
              setScopeFilter(val);
              resetPage();
            }}
            style={{ width: 180 }}
          >
            <Select.Option value='global'>Global</Select.Option>
            <Select.Option value='project'>Project</Select.Option>
            <Select.Option value='container'>Container</Select.Option>
            <Select.Option value='dataset'>Dataset</Select.Option>
          </Select>
        </Space>
        <Space>
          <Button
            type='primary'
            icon={<PlusOutlined />}
            onClick={() => setCreateModalOpen(true)}
          >
            Create Role
          </Button>
          <Button
            icon={<ReloadOutlined />}
            onClick={() =>
              queryClient.invalidateQueries({ queryKey: ['admin-roles'] })
            }
          >
            Refresh
          </Button>
        </Space>
      </div>

      <Table<RoleRecord>
        rowKey='id'
        columns={columns}
        dataSource={roles}
        loading={isLoading}
        expandable={{
          expandedRowRender: (record) => (
            <RolePermissionsView roleId={record.id} />
          ),
          rowExpandable: () => true,
        }}
        pagination={{
          current: page,
          pageSize,
          total,
          showSizeChanger: true,
          showTotal: (t) => `Total ${t} roles`,
          onChange: onPageChange,
        }}
        size='middle'
      />

      <CreateRoleModal
        open={createModalOpen}
        onClose={() => setCreateModalOpen(false)}
        onSubmit={handleCreateRole}
        loading={createRoleMutation.isPending}
      />
    </>
  );
};

export default RolesTab;
