import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import {
  CloseOutlined,
  ContainerOutlined,
  FormOutlined,
  GlobalOutlined,
  SaveOutlined,
  TagsOutlined,
} from '@ant-design/icons';
import { ContainerType } from '@rcabench/client';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Button,
  Card,
  Col,
  Divider,
  Form,
  Input,
  message,
  Row,
  Select,
  Space,
  Switch,
  Tag,
  Typography,
} from 'antd';

import { containerApi } from '@/api/containers';
import { useUnsavedChangesGuard } from '@/hooks/useUnsavedChangesGuard';

const { Title, Text } = Typography;
const { TextArea } = Input;
const { Option } = Select;

interface LabelItemWithKey {
  key: string;
  value?: string;
}

interface ContainerFormData {
  name: string;
  type: ContainerType;
  readme?: string;
  is_public: boolean;
  labels?: LabelItemWithKey[];
}

const ContainerForm = () => {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [form] = Form.useForm<ContainerFormData>();
  const { id } = useParams<{ id: string }>();
  const containerId = id ? Number(id) : undefined;
  const [labelInput, setLabelInput] = useState('');
  const [labels, setLabels] = useState<LabelItemWithKey[]>([]);
  const [isDirty, setIsDirty] = useState(false);
  useUnsavedChangesGuard(isDirty);

  // Fetch container data if editing
  const { data: containerData, isLoading } = useQuery({
    queryKey: ['container', containerId],
    queryFn: () => containerApi.getContainer(containerId as number),
    enabled: !!containerId,
  });

  // Set form data when editing
  useEffect(() => {
    if (containerData) {
      // Convert type to ContainerType enum safely
      let typeValue: ContainerType | undefined;
      if (containerData.type !== undefined) {
        const numValue = Number(containerData.type);
        if (!Number.isNaN(numValue)) {
          typeValue = numValue as ContainerType;
        } else if (typeof containerData.type === 'string') {
          // Look up string enum name (e.g. "Algorithm") in the ContainerType enum
          typeValue =
            ContainerType[containerData.type as keyof typeof ContainerType];
        }
      }

      form.setFieldsValue({
        name: containerData.name,
        type: typeValue,
        readme: containerData.readme,
        is_public: containerData.is_public,
      });
      // Filter out labels with undefined keys
      const validLabels = (containerData.labels || []).filter(
        (l): l is { key: string; value?: string } => l.key !== undefined
      );
      setLabels(validLabels);
    }
  }, [containerData, form]);

  // Create or update mutation
  const createMutation = useMutation({
    mutationFn: (data: ContainerFormData) => containerApi.createContainer(data),
    onSuccess: () => {
      message.success('Container created successfully');
      setIsDirty(false);
      queryClient.invalidateQueries({ queryKey: ['containers'] });
      navigate('/admin/containers');
    },
    onError: () => {
      message.error('Failed to create container');
    },
  });

  const updateMutation = useMutation({
    mutationFn: (data: Partial<ContainerFormData>) =>
      containerApi.updateContainer(containerId as number, data),
    onSuccess: () => {
      message.success('Container updated successfully');
      setIsDirty(false);
      queryClient.invalidateQueries({ queryKey: ['containers'] });
      queryClient.invalidateQueries({ queryKey: ['container', containerId] });
      navigate('/admin/containers');
    },
    onError: () => {
      message.error('Failed to update container');
    },
  });

  const handleSubmit = async (values: ContainerFormData) => {
    const data = {
      ...values,
      labels,
    };

    if (containerId) {
      updateMutation.mutate(data);
    } else {
      createMutation.mutate(data);
    }
  };

  const handleCancel = () => {
    navigate('/admin/containers');
  };

  const addLabel = () => {
    if (!labelInput.trim()) return;

    const [key, value] = labelInput.split(':').map((s) => s.trim());
    if (!key || !value) {
      message.warning('Please enter label in format: key:value');
      return;
    }

    if (labels.some((l) => l.key === key)) {
      message.warning('Label key already exists');
      return;
    }

    setLabels([...labels, { key, value }]);
    setLabelInput('');
  };

  const removeLabel = (key: string) => {
    setLabels(labels.filter((l) => l.key !== key));
  };

  if (isLoading && containerId) {
    return (
      <div style={{ padding: 24 }}>
        <Card loading>
          <div style={{ minHeight: 400 }} />
        </Card>
      </div>
    );
  }

  return (
    <div style={{ padding: 24 }}>
      {/* Header */}
      <div style={{ marginBottom: 24 }}>
        <Space>
          <Button icon={<CloseOutlined />} onClick={handleCancel}>
            Back to List
          </Button>
          <Title level={4} style={{ margin: 0 }}>
            {containerId ? 'Edit Container' : 'Create Container'}
          </Title>
        </Space>
      </div>

      <Row gutter={[24, 24]}>
        <Col xs={24} lg={16}>
          <Card
            title={
              <Space>
                <FormOutlined />
                <span>Container Info</span>
              </Space>
            }
          >
            <Form
              form={form}
              layout='vertical'
              onFinish={handleSubmit}
              onValuesChange={() => setIsDirty(true)}
              initialValues={{
                is_public: false,
              }}
            >
              <Form.Item
                label='Container Name'
                name='name'
                rules={[
                  { required: true, message: 'Please enter container name' },
                  { min: 3, message: 'Name must be at least 3 characters' },
                  { max: 50, message: 'Name cannot exceed 50 characters' },
                  {
                    pattern: /^[a-zA-Z0-9-_]+$/,
                    message:
                      'Name can only contain letters, numbers, hyphens and underscores',
                  },
                ]}
              >
                <Input
                  placeholder='Enter container name'
                  size='large'
                  disabled={!!containerId}
                />
              </Form.Item>

              <Form.Item
                label='Container Type'
                name='type'
                rules={[
                  { required: true, message: 'Please select container type' },
                ]}
              >
                <Select
                  placeholder='Select container type'
                  size='large'
                  onChange={() => {
                    form.validateFields(['type']);
                  }}
                >
                  <Option value={ContainerType.Pedestal}>
                    <Space>
                      <ContainerOutlined
                        style={{ color: 'var(--color-primary-500)' }}
                      />
                      <div>
                        <div>Pedestal</div>
                        <Text type='secondary' style={{ fontSize: '0.75rem' }}>
                          Base microservice environment for fault injection and
                          observation
                        </Text>
                      </div>
                    </Space>
                  </Option>
                  <Option value={ContainerType.Benchmark}>
                    <Space>
                      <ContainerOutlined
                        style={{ color: 'var(--color-success)' }}
                      />
                      <div>
                        <div>Benchmark</div>
                        <Text type='secondary' style={{ fontSize: '0.75rem' }}>
                          Benchmark container for load generation and evaluation
                        </Text>
                      </div>
                    </Space>
                  </Option>
                  <Option value={ContainerType.Algorithm}>
                    <Space>
                      <ContainerOutlined
                        style={{ color: 'var(--color-primary-700)' }}
                      />
                      <div>
                        <div>Algorithm</div>
                        <Text type='secondary' style={{ fontSize: '0.75rem' }}>
                          RCA algorithm container implementing root cause
                          analysis logic
                        </Text>
                      </div>
                    </Space>
                  </Option>
                </Select>
              </Form.Item>

              <Form.Item
                label='README'
                name='readme'
                rules={[
                  {
                    max: 5000,
                    message: 'README cannot exceed 5000 characters',
                  },
                ]}
              >
                <TextArea
                  rows={6}
                  placeholder='Enter container documentation and usage instructions...'
                />
              </Form.Item>

              <Form.Item
                label='Visibility'
                name='is_public'
                valuePropName='checked'
                help='Public containers can be used by other users in their projects'
              >
                <Switch
                  checkedChildren={<GlobalOutlined />}
                  unCheckedChildren={<GlobalOutlined />}
                />
              </Form.Item>

              <Divider />

              <Form.Item label='Labels'>
                <Space direction='vertical' style={{ width: '100%' }}>
                  <Space.Compact style={{ width: '100%' }}>
                    <Input
                      placeholder='Enter label (key:value)'
                      value={labelInput}
                      onChange={(e) => setLabelInput(e.target.value)}
                      onPressEnter={addLabel}
                    />
                    <Button
                      type='primary'
                      onClick={addLabel}
                      icon={<TagsOutlined />}
                    >
                      Add
                    </Button>
                  </Space.Compact>
                  <div>
                    {labels.map((label) => (
                      <Tag
                        key={label.key}
                        closable
                        onClose={() => removeLabel(label.key)}
                        icon={<TagsOutlined />}
                        style={{ marginBottom: 8 }}
                      >
                        {label.key}: {label.value}
                      </Tag>
                    ))}
                  </div>
                </Space>
              </Form.Item>

              <Form.Item>
                <Space>
                  <Button
                    type='primary'
                    htmlType='submit'
                    icon={<SaveOutlined />}
                    loading={
                      createMutation.isPending || updateMutation.isPending
                    }
                  >
                    {containerId ? 'Update Container' : 'Create Container'}
                  </Button>
                  <Button icon={<CloseOutlined />} onClick={handleCancel}>
                    Cancel
                  </Button>
                </Space>
              </Form.Item>
            </Form>
          </Card>
        </Col>

        <Col xs={24} lg={8}>
          <Card
            title={
              <Space>
                <ContainerOutlined />
                <span>Container Guide</span>
              </Space>
            }
          >
            <Space direction='vertical' style={{ width: '100%' }}>
              <div>
                <Text strong>Container Types:</Text>
                <ul style={{ marginTop: 8, marginBottom: 16 }}>
                  <li>
                    <Text>
                      <strong>Pedestal:</strong> Base microservice environment
                      for fault injection and observation
                    </Text>
                  </li>
                  <li>
                    <Text>
                      <strong>Benchmark:</strong> Benchmark container for load
                      generation and evaluation
                    </Text>
                  </li>
                  <li>
                    <Text>
                      <strong>Algorithm:</strong> RCA algorithm container
                      implementing root cause analysis logic
                    </Text>
                  </li>
                </ul>
              </div>

              <Divider />

              <div>
                <Text strong>Best Practices:</Text>
                <ul style={{ marginTop: 8 }}>
                  <li>
                    <Text>Use descriptive names</Text>
                  </li>
                  <li>
                    <Text>Write clear README documentation</Text>
                  </li>
                  <li>
                    <Text>Add appropriate labels for categorization</Text>
                  </li>
                  <li>
                    <Text>Maintain container version management</Text>
                  </li>
                  <li>
                    <Text>Test container image availability</Text>
                  </li>
                </ul>
              </div>

              <Divider />

              <div>
                <Text strong>Labels:</Text>
                <Text
                  type='secondary'
                  style={{ display: 'block', marginTop: 4 }}
                >
                  Use labels to organize and categorize your containers. Format:
                  key:value
                </Text>
              </div>

              <Divider />

              <div>
                <Text strong>Version Management:</Text>
                <Text
                  type='secondary'
                  style={{ display: 'block', marginTop: 4 }}
                >
                  After creating a container, you can add multiple versions to
                  track different iterations of the container image.
                </Text>
              </div>
            </Space>
          </Card>
        </Col>
      </Row>
    </div>
  );
};

export default ContainerForm;
