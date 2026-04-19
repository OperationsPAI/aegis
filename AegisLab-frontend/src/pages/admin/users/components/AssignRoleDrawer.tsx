import { useCallback, useMemo } from 'react';

import { TeamOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import {
  Button,
  Card,
  Drawer,
  Form,
  Select,
  Space,
  Tag,
  Typography,
} from 'antd';

import { roleApi } from '@/api/roles';

import type { RoleRecord, UserRecord } from '../types';

const { Text } = Typography;

interface AssignRoleDrawerProps {
  open: boolean;
  selectedUser: UserRecord | null;
  onClose: () => void;
  onAssign: (userId: number, roleId: number) => void;
  loading?: boolean;
}

const AssignRoleDrawer: React.FC<AssignRoleDrawerProps> = ({
  open,
  selectedUser,
  onClose,
  onAssign,
  loading,
}) => {
  const [assignForm] = Form.useForm();

  const { data: rolesData } = useQuery({
    queryKey: ['roles-all'],
    queryFn: () => roleApi.getRoles({ page: 1, size: 100 }),
    staleTime: 60_000,
  });

  const availableRoles: RoleRecord[] = useMemo(() => {
    if (!rolesData?.items) return [];
    // Backend enriches RoleResp with scope/description; RoleRecord extends RoleResp
    return rolesData.items as RoleRecord[];
  }, [rolesData]);

  const handleAssignRole = useCallback(
    (values: { roleId: number }) => {
      if (!selectedUser) return;
      onAssign(selectedUser.id, values.roleId);
      assignForm.resetFields();
    },
    [selectedUser, onAssign, assignForm]
  );

  const handleClose = useCallback(() => {
    onClose();
    assignForm.resetFields();
  }, [onClose, assignForm]);

  return (
    <Drawer
      title={`Assign Role to ${selectedUser?.username ?? ''}`}
      open={open}
      onClose={handleClose}
      width={400}
      extra={
        <Button
          type='primary'
          onClick={() => assignForm.submit()}
          loading={loading}
        >
          Assign
        </Button>
      }
    >
      <Form form={assignForm} layout='vertical' onFinish={handleAssignRole}>
        <Form.Item
          name='roleId'
          label='Role'
          rules={[{ required: true, message: 'Please select a role' }]}
        >
          <Select
            placeholder='Select a role to assign'
            showSearch
            optionFilterProp='label'
          >
            {availableRoles.map((role) => (
              <Select.Option key={role.id} value={role.id} label={role.name}>
                <Space>
                  <TeamOutlined />
                  <span>{role.name}</span>
                  {role.scope && (
                    <Tag style={{ fontSize: 10 }}>{role.scope}</Tag>
                  )}
                </Space>
              </Select.Option>
            ))}
          </Select>
        </Form.Item>

        {selectedUser && (
          <Card size='small' title='Current Roles' style={{ marginTop: 16 }}>
            {selectedUser.roles && selectedUser.roles.length > 0 ? (
              <Space wrap>
                {selectedUser.roles.map((role) => (
                  <Tag key={role.id} color='blue'>
                    {role.name}
                  </Tag>
                ))}
              </Space>
            ) : (
              <Text type='secondary'>No roles assigned yet</Text>
            )}
          </Card>
        )}
      </Form>
    </Drawer>
  );
};

export default AssignRoleDrawer;
