import { useState } from 'react';

import type { PermissionResp } from '@rcabench/client';
import { Checkbox, Modal, Space, Spin, Tag, Typography } from 'antd';

const { Text } = Typography;

interface AssignPermissionsModalProps {
  open: boolean;
  onClose: () => void;
  onAssign: (permissionIds: number[]) => void;
  loading?: boolean;
  permissionsLoading?: boolean;
  availablePermissions: PermissionResp[];
}

const AssignPermissionsModal: React.FC<AssignPermissionsModalProps> = ({
  open,
  onClose,
  onAssign,
  loading,
  permissionsLoading,
  availablePermissions,
}) => {
  const [selectedIds, setSelectedIds] = useState<number[]>([]);

  const handleClose = () => {
    onClose();
    setSelectedIds([]);
  };

  return (
    <Modal
      title='Assign Permissions'
      open={open}
      onCancel={handleClose}
      onOk={() => onAssign(selectedIds)}
      okText='Assign Selected'
      confirmLoading={loading}
      width={600}
    >
      {permissionsLoading ? (
        <div style={{ textAlign: 'center', padding: 24 }}>
          <Spin />
        </div>
      ) : availablePermissions.length === 0 ? (
        <Text type='secondary'>
          No additional permissions available to assign.
        </Text>
      ) : (
        <div style={{ maxHeight: 400, overflow: 'auto' }}>
          <Checkbox.Group
            value={selectedIds}
            onChange={(values) => setSelectedIds(values as number[])}
            style={{ display: 'flex', flexDirection: 'column', gap: 8 }}
          >
            {availablePermissions.map((perm) => (
              <Checkbox key={perm.id} value={perm.id}>
                <Space>
                  <Text strong>{perm.display_name ?? perm.name ?? '-'}</Text>
                  {perm.action && (
                    <Tag
                      color={
                        {
                          create: 'green',
                          read: 'blue',
                          update: 'orange',
                          delete: 'red',
                          manage: 'purple',
                        }[perm.action.toLowerCase()] ?? 'default'
                      }
                    >
                      {perm.action}
                    </Tag>
                  )}
                  {perm.resource_name && (
                    <Text type='secondary'>{perm.resource_name}</Text>
                  )}
                </Space>
              </Checkbox>
            ))}
          </Checkbox.Group>
        </div>
      )}
    </Modal>
  );
};

export default AssignPermissionsModal;
