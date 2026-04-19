import { useMemo, useState } from 'react';

import { DeleteOutlined, LockOutlined, PlusOutlined } from '@ant-design/icons';
import type { PermissionResp } from '@rcabench/client';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Button,
  message,
  Popconfirm,
  Space,
  Spin,
  Table,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';

import { permissionApi } from '@/api/permissions';
import { roleApi } from '@/api/roles';

import AssignPermissionsModal from './components/AssignPermissionsModal';

const { Text } = Typography;

const RolePermissionsView: React.FC<{ roleId: number }> = ({ roleId }) => {
  const queryClient = useQueryClient();
  const [assignModalOpen, setAssignModalOpen] = useState(false);

  const { data: roleDetail, isLoading: roleDetailLoading } = useQuery({
    queryKey: ['role-detail', roleId],
    queryFn: () => roleApi.getRole(roleId),
    staleTime: 10_000,
  });

  const { data: allPermissions, isLoading: permissionsLoading } = useQuery({
    queryKey: ['all-permissions'],
    queryFn: () => permissionApi.getPermissions({ page: 1, size: 500 }),
    staleTime: 30_000,
    enabled: assignModalOpen,
  });

  const removePermissionsMutation = useMutation({
    mutationFn: (permissionIds: number[]) =>
      roleApi.removePermissions(roleId, { permission_ids: permissionIds }),
    onSuccess: () => {
      message.success('Permission removed successfully');
      queryClient.invalidateQueries({ queryKey: ['role-detail', roleId] });
      queryClient.invalidateQueries({ queryKey: ['admin-roles'] });
    },
    onError: () => {
      message.error('Failed to remove permission');
    },
  });

  const assignPermissionsMutation = useMutation({
    mutationFn: (permissionIds: number[]) =>
      roleApi.assignPermissions(roleId, { permission_ids: permissionIds }),
    onSuccess: () => {
      message.success('Permissions assigned successfully');
      setAssignModalOpen(false);
      queryClient.invalidateQueries({ queryKey: ['role-detail', roleId] });
      queryClient.invalidateQueries({ queryKey: ['admin-roles'] });
    },
    onError: () => {
      message.error('Failed to assign permissions');
    },
  });

  const currentPermissions: PermissionResp[] = useMemo(
    () => roleDetail?.permissions ?? [],
    [roleDetail]
  );

  const currentPermissionIds = useMemo(
    () => new Set(currentPermissions.map((p) => p.id).filter(Boolean)),
    [currentPermissions]
  );

  const availablePermissions = useMemo(() => {
    if (!allPermissions) return [];
    return allPermissions.filter(
      (p) => p.id != null && !currentPermissionIds.has(p.id)
    );
  }, [allPermissions, currentPermissionIds]);

  if (roleDetailLoading) {
    return (
      <div style={{ padding: 16, textAlign: 'center' }}>
        <Spin size='small' />
      </div>
    );
  }

  const permissionColumns: ColumnsType<PermissionResp> = [
    {
      title: 'Permission',
      dataIndex: 'display_name',
      key: 'display_name',
      width: 200,
      render: (text: string | undefined, record: PermissionResp) => (
        <Space>
          <LockOutlined />
          <Text strong>{text ?? record.name ?? '-'}</Text>
        </Space>
      ),
    },
    {
      title: 'Action',
      dataIndex: 'action',
      key: 'action',
      width: 120,
      render: (action?: string) => {
        if (!action) return <Text type='secondary'>-</Text>;
        const colorMap: Record<string, string> = {
          create: 'green',
          read: 'blue',
          update: 'orange',
          delete: 'red',
          manage: 'purple',
        };
        return (
          <Tag color={colorMap[action.toLowerCase()] ?? 'default'}>
            {action}
          </Tag>
        );
      },
    },
    {
      title: 'Resource',
      dataIndex: 'resource_name',
      key: 'resource_name',
      width: 150,
      render: (name?: string) => <Text type='secondary'>{name ?? '-'}</Text>,
    },
    {
      title: 'Scope',
      dataIndex: 'scope',
      key: 'scope',
      width: 100,
      render: (scope?: string) =>
        scope ? <Tag>{scope}</Tag> : <Text type='secondary'>-</Text>,
    },
    {
      title: '',
      key: 'remove',
      width: 60,
      render: (_: unknown, record: PermissionResp) => (
        <Popconfirm
          title='Remove this permission?'
          okText='Remove'
          okButtonProps={{ danger: true }}
          onConfirm={() => {
            if (record.id != null) {
              removePermissionsMutation.mutate([record.id]);
            }
          }}
        >
          <Button type='text' size='small' danger icon={<DeleteOutlined />} />
        </Popconfirm>
      ),
    },
  ];

  const handleAssign = (permissionIds: number[]) => {
    if (permissionIds.length === 0) {
      message.error('Please select at least one permission');
      return;
    }
    assignPermissionsMutation.mutate(permissionIds);
  };

  return (
    <div style={{ padding: '8px 0' }}>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 12,
        }}
      >
        <Text strong>
          <LockOutlined style={{ marginRight: 4 }} />
          Permissions ({currentPermissions.length})
        </Text>
        <Button
          type='primary'
          size='small'
          icon={<PlusOutlined />}
          onClick={() => setAssignModalOpen(true)}
        >
          Assign Permissions
        </Button>
      </div>

      {currentPermissions.length > 0 ? (
        <Table<PermissionResp>
          rowKey='id'
          columns={permissionColumns}
          dataSource={currentPermissions}
          pagination={false}
          size='small'
        />
      ) : (
        <Text type='secondary'>No permissions assigned to this role.</Text>
      )}

      <AssignPermissionsModal
        open={assignModalOpen}
        onClose={() => setAssignModalOpen(false)}
        onAssign={handleAssign}
        loading={assignPermissionsMutation.isPending}
        permissionsLoading={permissionsLoading}
        availablePermissions={availablePermissions}
      />
    </div>
  );
};

export default RolePermissionsView;
