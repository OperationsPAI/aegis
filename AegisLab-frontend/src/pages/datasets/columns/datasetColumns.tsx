import {
  ClockCircleOutlined,
  DatabaseOutlined,
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  FileTextOutlined,
  LineChartOutlined,
  SettingOutlined,
} from '@ant-design/icons';
import type { DatasetResp } from '@rcabench/client';
import { Avatar, Badge, Button, Space, Tag, Tooltip, Typography } from 'antd';
import dayjs from 'dayjs';

const { Text } = Typography;

type DatasetType = 'Trace' | 'Log' | 'Metric';

const getTypeIcon = (type: DatasetType) => {
  switch (type) {
    case 'Trace':
      return <DatabaseOutlined style={{ color: 'var(--color-primary-500)' }} />;
    case 'Log':
      return <FileTextOutlined style={{ color: 'var(--color-success)' }} />;
    case 'Metric':
      return <LineChartOutlined style={{ color: 'var(--color-warning)' }} />;
    default:
      return <DatabaseOutlined />;
  }
};

const getTypeColor = (type: DatasetType) => {
  switch (type) {
    case 'Trace':
      return 'var(--color-primary-500)';
    case 'Log':
      return 'var(--color-success)';
    case 'Metric':
      return 'var(--color-warning)';
    default:
      return 'var(--color-secondary-500)';
  }
};

export function buildDatasetColumns(
  onView: (id: number) => void,
  onEdit: (id: number) => void,
  onManageVersions: (id: number) => void,
  onDelete: (id: number) => void
) {
  return [
    {
      title: 'Dataset',
      dataIndex: 'name',
      key: 'name',
      width: '30%',
      render: (name: string, record: DatasetResp) => (
        <Space>
          <Avatar
            size='large'
            style={{
              backgroundColor: getTypeColor(record.type as DatasetType),
              fontSize: '1.25rem',
            }}
            icon={getTypeIcon(record.type as DatasetType)}
          />
          <div>
            <Text strong style={{ fontSize: '1rem' }}>
              {name}
            </Text>
            <br />
            <Text type='secondary' style={{ fontSize: '0.875rem' }}>
              ID: {record.id}
            </Text>
          </div>
        </Space>
      ),
    },
    {
      title: 'Type',
      dataIndex: 'type',
      key: 'type',
      width: '15%',
      render: (type: string) => (
        <Tag
          color={getTypeColor(type as DatasetType)}
          style={{ fontWeight: 500 }}
        >
          {type}
        </Tag>
      ),
      filters: [
        { text: 'Trace', value: 'Trace' },
        { text: 'Log', value: 'Log' },
        { text: 'Metric', value: 'Metric' },
      ],
      onFilter: (value: unknown, record: DatasetResp) =>
        record.type === (value as string),
    },
    {
      title: 'Public',
      dataIndex: 'is_public',
      key: 'is_public',
      width: '10%',
      render: (isPublic: boolean) => (
        <Tag color={isPublic ? 'green' : 'default'}>
          {isPublic ? 'Public' : 'Private'}
        </Tag>
      ),
    },
    {
      title: 'Versions',
      dataIndex: 'versions',
      key: 'versions',
      width: '10%',
      render: (versions: Array<{ version?: string }> = []) => (
        <Badge
          count={versions.length}
          showZero
          style={{ backgroundColor: 'var(--color-primary-500)' }}
        />
      ),
    },
    {
      title: 'Description',
      dataIndex: 'description',
      key: 'description',
      width: '20%',
      render: (description?: string) =>
        description ? (
          <Tooltip title={description}>
            <Text ellipsis style={{ maxWidth: 200 }}>
              {description}
            </Text>
          </Tooltip>
        ) : (
          <Text type='secondary'>No description</Text>
        ),
    },
    {
      title: 'Created',
      dataIndex: 'created_at',
      key: 'created_at',
      width: '12%',
      render: (date: string) => (
        <Space>
          <ClockCircleOutlined />
          <Text>{dayjs(date).format('MMM D, YYYY')}</Text>
        </Space>
      ),
    },
    {
      title: 'Actions',
      key: 'actions',
      width: '18%',
      render: (_: unknown, record: DatasetResp) => (
        <Space>
          <Tooltip title='View Details'>
            <Button
              type='text'
              icon={<EyeOutlined />}
              onClick={() => record.id && onView(record.id)}
            />
          </Tooltip>
          <Tooltip title='Edit Dataset'>
            <Button
              type='text'
              icon={<EditOutlined />}
              onClick={() => record.id && onEdit(record.id)}
            />
          </Tooltip>
          <Tooltip title='Manage Versions'>
            <Button
              type='text'
              icon={<SettingOutlined />}
              onClick={() => record.id && onManageVersions(record.id)}
            />
          </Tooltip>
          <Tooltip title='Delete Dataset'>
            <Button
              type='text'
              danger
              icon={<DeleteOutlined />}
              onClick={() => record.id && onDelete(record.id)}
            />
          </Tooltip>
        </Space>
      ),
    },
  ];
}
