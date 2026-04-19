import { useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import {
  ArrowLeftOutlined,
  ClockCircleOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
} from '@ant-design/icons';
import type { ContainerVersionResp } from '@rcabench/client';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Badge,
  Button,
  Card,
  Form,
  Input,
  message,
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

import { containerApi } from '@/api/containers';

const { Title, Text } = Typography;
const { TextArea } = Input;

interface VersionFormData {
  name: string;
  image_ref: string;
  command?: string;
}

const ContainerVersions = () => {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { id } = useParams<{ id: string }>();
  const containerId = Number(id);
  const [isModalVisible, setIsModalVisible] = useState(false);
  const [editingVersion, setEditingVersion] =
    useState<ContainerVersionResp | null>(null);
  const [form] = Form.useForm<VersionFormData>();

  // Fetch container details
  const { data: container } = useQuery({
    queryKey: ['container', containerId],
    queryFn: () => containerApi.getContainer(containerId),
    enabled: !!containerId,
  });

  // Fetch versions
  const { data: versionsData, isLoading: versionsLoading } = useQuery({
    queryKey: ['container-versions', containerId],
    queryFn: () => containerApi.getVersions(containerId),
    enabled: !!containerId,
  });
  const versions = versionsData?.items || [];

  // Create version mutation
  const createVersionMutation = useMutation({
    mutationFn: (data: VersionFormData) =>
      containerApi.createVersion(containerId, data),
    onSuccess: () => {
      message.success('Version created successfully');
      queryClient.invalidateQueries({
        queryKey: ['container-versions', containerId],
      });
      setIsModalVisible(false);
      form.resetFields();
    },
    onError: () => {
      message.error('Failed to create version');
    },
  });

  // Update version mutation
  const updateVersionMutation = useMutation({
    mutationFn: ({
      versionId,
      data,
    }: {
      versionId: number;
      data: Partial<VersionFormData>;
    }) => containerApi.updateVersion(containerId, versionId, data),
    onSuccess: () => {
      message.success('Version updated successfully');
      queryClient.invalidateQueries({
        queryKey: ['container-versions', containerId],
      });
      setIsModalVisible(false);
      setEditingVersion(null);
      form.resetFields();
    },
    onError: () => {
      message.error('Failed to update version');
    },
  });

  // Delete version mutation
  const deleteVersionMutation = useMutation({
    mutationFn: (versionId: number) =>
      containerApi.deleteVersion(containerId, versionId),
    onSuccess: () => {
      message.success('Version deleted successfully');
      queryClient.invalidateQueries({
        queryKey: ['container-versions', containerId],
      });
    },
    onError: () => {
      message.error('Failed to delete version');
    },
  });

  const handleCreateVersion = () => {
    setEditingVersion(null);
    form.resetFields();
    setIsModalVisible(true);
  };

  const handleEditVersion = (version: ContainerVersionResp) => {
    setEditingVersion(version);
    form.setFieldsValue({
      name: version.name || '',
      image_ref: version.image_ref || '',
    });
    setIsModalVisible(true);
  };

  const handleDeleteVersion = (versionId: number | undefined) => {
    if (versionId !== undefined) {
      deleteVersionMutation.mutate(versionId);
    }
  };

  const handleModalOk = () => {
    form.validateFields().then((values) => {
      if (editingVersion && editingVersion.id !== undefined) {
        updateVersionMutation.mutate({
          versionId: editingVersion.id,
          data: values,
        });
      } else {
        createVersionMutation.mutate(values);
      }
    });
  };

  const handleModalCancel = () => {
    setIsModalVisible(false);
    setEditingVersion(null);
    form.resetFields();
  };

  const columns: ColumnsType<ContainerVersionResp> = [
    {
      title: 'Version',
      dataIndex: 'name',
      key: 'name',
      width: 120,
      render: (name: string) => (
        <Badge
          count={name}
          style={{
            backgroundColor: 'var(--color-primary-500)',
            fontWeight: 'bold',
          }}
        />
      ),
    },
    {
      title: 'Image Reference',
      dataIndex: 'image_ref',
      key: 'image_ref',
      render: (imageRef: string) => (
        <Tooltip title={imageRef}>
          <Text code ellipsis style={{ maxWidth: 300, fontSize: '0.875rem' }}>
            {imageRef}
          </Text>
        </Tooltip>
      ),
    },
    {
      title: 'Usage Count',
      dataIndex: 'usage',
      key: 'usage',
      width: 100,
      render: (usage?: number) => <Tag color='blue'>{usage ?? 0}</Tag>,
    },
    {
      title: 'Updated',
      dataIndex: 'updated_at',
      key: 'updated_at',
      width: 180,
      render: (date: string) => (
        <Space>
          <ClockCircleOutlined />
          <Text>{dayjs(date).format('YYYY-MM-DD HH:mm')}</Text>
        </Space>
      ),
    },
    {
      title: 'Actions',
      key: 'actions',
      width: 150,
      fixed: 'right' as const,
      render: (_, record) => (
        <Space>
          <Button
            type='link'
            size='small'
            icon={<EditOutlined />}
            onClick={() => handleEditVersion(record)}
          >
            Edit
          </Button>
          <Popconfirm
            title='Confirm Delete'
            description='Are you sure you want to delete this version?'
            onConfirm={() => handleDeleteVersion(record.id)}
            okText='Confirm'
            cancelText='Cancel'
          >
            <Button
              type='link'
              size='small'
              danger
              icon={<DeleteOutlined />}
              loading={deleteVersionMutation.isPending}
            />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div style={{ padding: 24 }}>
      {/* Header */}
      <div style={{ marginBottom: 24 }}>
        <Space>
          <Button
            icon={<ArrowLeftOutlined />}
            onClick={() => navigate(`/admin/containers/${containerId}`)}
          >
            Back to Container
          </Button>
          <Title level={4} style={{ margin: 0 }}>
            {container?.name} - Version Management
          </Title>
        </Space>
      </div>

      {/* Versions Table */}
      <Card
        title='Container Versions'
        extra={
          <Button
            type='primary'
            icon={<PlusOutlined />}
            onClick={handleCreateVersion}
          >
            Add Version
          </Button>
        }
      >
        <Table
          rowKey='id'
          columns={columns}
          dataSource={versions}
          loading={versionsLoading}
          pagination={{
            pageSize: 10,
            showSizeChanger: true,
            showQuickJumper: true,
            showTotal: (total, range) =>
              `${range[0]}-${range[1]} of ${total} versions`,
          }}
        />
      </Card>

      {/* Create/Edit Version Modal */}
      <Modal
        title={editingVersion ? 'Edit Version' : 'Add Version'}
        open={isModalVisible}
        onOk={handleModalOk}
        onCancel={handleModalCancel}
        confirmLoading={
          createVersionMutation.isPending || updateVersionMutation.isPending
        }
        width={600}
      >
        <Form form={form} layout='vertical' style={{ marginTop: 24 }}>
          <Form.Item
            label='Version Name'
            name='name'
            rules={[
              { required: true, message: 'Please enter version name' },
              {
                pattern: /^[a-zA-Z0-9._-]+$/,
                message:
                  'Version name can only contain letters, numbers, dots, underscores and hyphens',
              },
            ]}
          >
            <Input placeholder='v1.0.0 or latest' />
          </Form.Item>

          <Form.Item
            label='Image Reference'
            name='image_ref'
            rules={[
              {
                required: true,
                message: 'Please enter the full image reference',
              },
            ]}
          >
            <Input placeholder='docker.io/username/image:tag' />
          </Form.Item>

          <Form.Item label='Start Command (optional)' name='command'>
            <TextArea
              rows={3}
              placeholder='Container start command or arguments'
            />
          </Form.Item>

          <div
            style={{
              padding: 12,
              background: 'var(--color-bg-secondary)',
              borderRadius: 4,
              marginTop: 16,
            }}
          >
            <Text type='secondary' style={{ fontSize: '0.875rem' }}>
              <strong>Tip:</strong> Image reference format:
              <br />
              <code>registry/repository:tag</code>
              <br />
              Example: <code>docker.io/library/nginx:latest</code>
            </Text>
          </div>
        </Form>
      </Modal>
    </div>
  );
};

export default ContainerVersions;
