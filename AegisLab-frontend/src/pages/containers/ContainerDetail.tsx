import { useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import {
  ArrowLeftOutlined,
  BuildOutlined,
  ClockCircleOutlined,
  CloudUploadOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  TagsOutlined,
} from '@ant-design/icons';
import type { ContainerVersionResp, LabelItem } from '@rcabench/client';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Badge,
  Button,
  Card,
  Col,
  Descriptions,
  Divider,
  message,
  Modal,
  Row,
  Space,
  Table,
  Tabs,
  Tag,
  Tooltip,
  Typography,
  Upload,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import dayjs from 'dayjs';

import { containerApi } from '@/api/containers';

const { Title, Text } = Typography;

const ContainerDetail = () => {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { id } = useParams<{ id: string }>();
  const containerId = Number(id);
  const [activeTab, setActiveTab] = useState('overview');
  const [helmUploadVersionId, setHelmUploadVersionId] = useState<number | null>(
    null
  );
  const [helmUploadType, setHelmUploadType] = useState<'chart' | 'values'>(
    'chart'
  );
  const [helmModalVisible, setHelmModalVisible] = useState(false);

  // Fetch container details
  const { data: container, isLoading } = useQuery({
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

  // Build container mutation
  const buildMutation = useMutation({
    mutationFn: () =>
      containerApi.buildContainer({ container_id: containerId }),
    onSuccess: () => {
      message.success('Container build started');
      queryClient.invalidateQueries({
        queryKey: ['container', containerId],
      });
    },
    onError: () => {
      message.error('Failed to start container build');
    },
  });

  // Helm chart upload mutation
  const helmChartMutation = useMutation({
    mutationFn: ({
      versionId,
      formData,
    }: {
      versionId: number;
      formData: FormData;
    }) => containerApi.uploadHelmChart(containerId, versionId, formData),
    onSuccess: () => {
      message.success('Helm chart uploaded successfully');
      setHelmModalVisible(false);
      queryClient.invalidateQueries({
        queryKey: ['container-versions', containerId],
      });
    },
    onError: () => {
      message.error('Failed to upload Helm chart');
    },
  });

  // Helm values upload mutation
  const helmValuesMutation = useMutation({
    mutationFn: ({
      versionId,
      formData,
    }: {
      versionId: number;
      formData: FormData;
    }) => containerApi.uploadHelmValues(containerId, versionId, formData),
    onSuccess: () => {
      message.success('Helm values uploaded successfully');
      setHelmModalVisible(false);
      queryClient.invalidateQueries({
        queryKey: ['container-versions', containerId],
      });
    },
    onError: () => {
      message.error('Failed to upload Helm values');
    },
  });

  const handleBuild = () => {
    Modal.confirm({
      title: 'Build Container',
      content: `Are you sure you want to build container "${container?.name}"?`,
      okText: 'Build',
      onOk: () => buildMutation.mutate(),
    });
  };

  const handleHelmUpload = (file: File) => {
    if (!helmUploadVersionId) return;
    const formData = new FormData();
    formData.append('file', file);
    if (helmUploadType === 'chart') {
      helmChartMutation.mutate({ versionId: helmUploadVersionId, formData });
    } else {
      helmValuesMutation.mutate({ versionId: helmUploadVersionId, formData });
    }
  };

  const openHelmUpload = (versionId: number, type: 'chart' | 'values') => {
    setHelmUploadVersionId(versionId);
    setHelmUploadType(type);
    setHelmModalVisible(true);
  };

  const handleEdit = () => {
    navigate(`/admin/containers/${containerId}/edit`);
  };

  const handleDelete = () => {
    Modal.confirm({
      title: 'Delete Container',
      content: `Are you sure you want to delete container "${container?.name}"? This action cannot be undone.`,
      okText: 'Delete',
      okButtonProps: { danger: true },
      cancelText: 'Cancel',
      onOk: async () => {
        try {
          await containerApi.deleteContainer(containerId);
          message.success('Container deleted successfully');
          navigate('/admin/containers');
        } catch (error) {
          message.error('Failed to delete container');
        }
      },
    });
  };

  const getTypeColor = (type: string | undefined) => {
    switch (type) {
      case 'Pedestal':
        return 'blue';
      case 'Benchmark':
        return 'green';
      case 'Algorithm':
        return 'purple';
      default:
        return 'default';
    }
  };

  const versionColumns: ColumnsType<ContainerVersionResp> = [
    {
      title: 'Version',
      dataIndex: 'version',
      key: 'version',
      width: 120,
      render: (version: string) => (
        <Badge
          count={version}
          style={{
            backgroundColor: 'var(--color-primary-500)',
            fontWeight: 'bold',
          }}
        />
      ),
    },
    {
      title: 'Registry',
      dataIndex: 'registry',
      key: 'registry',
      render: (registry: string) => (
        <Text code style={{ fontSize: '0.875rem' }}>
          {registry}
        </Text>
      ),
    },
    {
      title: 'Repository',
      dataIndex: 'repository',
      key: 'repository',
      render: (repository: string) => (
        <Tooltip title={repository}>
          <Text ellipsis style={{ maxWidth: 200 }}>
            {repository}
          </Text>
        </Tooltip>
      ),
    },
    {
      title: 'Tag',
      dataIndex: 'tag',
      key: 'tag',
      width: 120,
      render: (tag: string) => <Tag color='blue'>{tag}</Tag>,
    },
    {
      title: 'Created',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 180,
      render: (date: string) => (
        <Space>
          <ClockCircleOutlined />
          <Text>{dayjs(date).format('YYYY-MM-DD HH:mm')}</Text>
        </Space>
      ),
    },
    {
      title: 'Helm',
      key: 'helm',
      width: 200,
      render: (_: unknown, record: ContainerVersionResp) => (
        <Space>
          <Button
            size='small'
            icon={<CloudUploadOutlined />}
            onClick={() =>
              record.id != null && openHelmUpload(record.id, 'chart')
            }
          >
            Chart
          </Button>
          <Button
            size='small'
            icon={<CloudUploadOutlined />}
            onClick={() =>
              record.id != null && openHelmUpload(record.id, 'values')
            }
          >
            Values
          </Button>
        </Space>
      ),
    },
  ];

  if (isLoading) {
    return (
      <div style={{ padding: 24 }}>
        <Card loading>
          <div style={{ minHeight: 400 }} />
        </Card>
      </div>
    );
  }

  if (!container) {
    return (
      <div style={{ padding: 24, textAlign: 'center' }}>
        <Text type='secondary'>Container not found</Text>
      </div>
    );
  }

  return (
    <div style={{ padding: 24 }}>
      {/* Header */}
      <div style={{ marginBottom: 24 }}>
        <Space>
          <Button
            icon={<ArrowLeftOutlined />}
            onClick={() => navigate('/admin/containers')}
          >
            Back to List
          </Button>
          <Title level={4} style={{ margin: 0 }}>
            {container.name}
          </Title>
        </Space>
      </div>

      {/* Actions */}
      <Card style={{ marginBottom: 24 }}>
        <Row justify='space-between' align='middle'>
          <Col>
            <Space>
              <Button
                type='primary'
                icon={<EditOutlined />}
                onClick={handleEdit}
              >
                Edit Container
              </Button>
              <Button
                icon={<BuildOutlined />}
                onClick={handleBuild}
                loading={buildMutation.isPending}
              >
                Build
              </Button>
              <Button
                icon={<PlusOutlined />}
                onClick={() => navigate(`/admin/containers/${containerId}/versions`)}
              >
                Manage Versions
              </Button>
            </Space>
          </Col>
          <Col>
            <Button danger icon={<DeleteOutlined />} onClick={handleDelete}>
              Delete Container
            </Button>
          </Col>
        </Row>
      </Card>

      {/* Tabs */}
      <Tabs
        activeKey={activeTab}
        onChange={setActiveTab}
        items={[
          {
            key: 'overview',
            label: 'Overview',
            children: (
              <>
                <Row gutter={[16, 16]}>
                  <Col xs={24} lg={16}>
                    <Card title='Container Info'>
                      <Descriptions column={2} bordered>
                        <Descriptions.Item label='ID'>
                          {container.id}
                        </Descriptions.Item>
                        <Descriptions.Item label='Type'>
                          <Tag
                            color={getTypeColor(container.type)}
                            style={{ fontWeight: 500, fontSize: '1rem' }}
                          >
                            {container.type}
                          </Tag>
                        </Descriptions.Item>
                        <Descriptions.Item label='Visibility'>
                          <Tag color={container.is_public ? 'green' : 'orange'}>
                            {container.is_public ? 'Public' : 'Private'}
                          </Tag>
                        </Descriptions.Item>
                        <Descriptions.Item label='Created'>
                          <Space>
                            <ClockCircleOutlined />
                            {dayjs(container.created_at).format(
                              'YYYY-MM-DD HH:mm'
                            )}
                          </Space>
                        </Descriptions.Item>
                        <Descriptions.Item label='Updated'>
                          <Space>
                            <ClockCircleOutlined />
                            {dayjs(container.updated_at).format(
                              'YYYY-MM-DD HH:mm'
                            )}
                          </Space>
                        </Descriptions.Item>
                        <Descriptions.Item label='Labels'>
                          {container.labels?.length ? (
                            <Space wrap>
                              {container.labels.map(
                                (label: LabelItem, index: number) => (
                                  <Tag key={index} icon={<TagsOutlined />}>
                                    {label.key}: {label.value}
                                  </Tag>
                                )
                              )}
                            </Space>
                          ) : (
                            <Text type='secondary'>No labels</Text>
                          )}
                        </Descriptions.Item>
                      </Descriptions>
                    </Card>
                  </Col>
                  <Col xs={24} lg={8}>
                    <Card title='Quick Stats'>
                      <Space direction='vertical' style={{ width: '100%' }}>
                        <div>
                          <Text type='secondary'>Total Versions</Text>
                          <br />
                          <Title
                            level={5}
                            style={{
                              margin: 0,
                              color: 'var(--color-primary-500)',
                            }}
                          >
                            {versions.length}
                          </Title>
                        </div>
                        <Divider />
                        <div>
                          <Text type='secondary'>Latest Version</Text>
                          <br />
                          <Text strong style={{ fontSize: '1.25rem' }}>
                            {versions[0]?.name || 'N/A'}
                          </Text>
                        </div>
                      </Space>
                    </Card>
                  </Col>
                </Row>

                {container.readme && (
                  <Card title='README' style={{ marginTop: 16 }}>
                    <Text>{container.readme}</Text>
                  </Card>
                )}
              </>
            ),
          },
          {
            key: 'versions',
            label: 'Versions',
            children: (
              <Card
                title='Container Versions'
                extra={
                  <Button
                    type='primary'
                    icon={<PlusOutlined />}
                    onClick={() =>
                      navigate(`/admin/containers/${containerId}/versions`)
                    }
                  >
                    Manage Versions
                  </Button>
                }
              >
                <Table
                  rowKey='id'
                  columns={versionColumns}
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
            ),
          },
          {
            key: 'usage',
            label: 'Usage Guide',
            children: (
              <Card title='Container Usage Guide'>
                <Text>
                  This container can be used for experiments and evaluations.
                </Text>
                <Divider />
                <Text strong>How to use this container:</Text>
                <ul style={{ marginTop: 8 }}>
                  <li>Select this container when creating an experiment</li>
                  <li>Choose the appropriate version for deployment</li>
                  <li>
                    The container will be loaded automatically during execution
                  </li>
                  <li>Can be combined with other containers and datasets</li>
                </ul>
              </Card>
            ),
          },
        ]}
      />

      {/* Helm Upload Modal */}
      <Modal
        title={`Upload Helm ${helmUploadType === 'chart' ? 'Chart' : 'Values'}`}
        open={helmModalVisible}
        onCancel={() => setHelmModalVisible(false)}
        footer={null}
      >
        <Upload.Dragger
          accept={helmUploadType === 'chart' ? '.tgz,.tar.gz' : '.yaml,.yml'}
          beforeUpload={(file) => {
            handleHelmUpload(file);
            return false; // Prevent auto upload
          }}
          showUploadList={false}
        >
          <p className='ant-upload-drag-icon'>
            <CloudUploadOutlined />
          </p>
          <p className='ant-upload-text'>
            Click or drag{' '}
            {helmUploadType === 'chart' ? 'chart (.tgz)' : 'values (.yaml)'}{' '}
            file to upload
          </p>
        </Upload.Dragger>
      </Modal>
    </div>
  );
};

export default ContainerDetail;
