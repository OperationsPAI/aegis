import { useMemo } from 'react';
import { Link, useParams } from 'react-router-dom';

import {
  CheckCircleOutlined,
  ClockCircleOutlined,
  CloseCircleOutlined,
  MinusCircleOutlined,
} from '@ant-design/icons';
import type {
  DetectorResultItem,
  ExecutionDetailResp,
  GranularityResultItem,
} from '@rcabench/client';
import { useQuery } from '@tanstack/react-query';
import {
  Badge,
  Card,
  Descriptions,
  Empty,
  Skeleton,
  Space,
  Table,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import dayjs from 'dayjs';

import { executionApi } from '@/api/executions';
import TaskLogViewer from '@/components/pipeline/TaskLogViewer';

const { Title, Text } = Typography;

/** Map execution state to display config */
const stateConfig: Record<
  string,
  { color: string; icon: React.ReactNode; label: string }
> = {
  Initial: {
    color: 'default',
    icon: <MinusCircleOutlined />,
    label: 'Initial',
  },
  Failed: {
    color: 'error',
    icon: <CloseCircleOutlined />,
    label: 'Failed',
  },
  Success: {
    color: 'success',
    icon: <CheckCircleOutlined />,
    label: 'Success',
  },
};

/** Format seconds to "Xm Ys" */
function formatDuration(seconds?: number): string {
  if (seconds == null) return '-';
  if (seconds < 60) return `${seconds}s`;
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m}m ${s}s`;
}

/**
 * Build table columns dynamically from an array of objects.
 * Each unique key across all items becomes a column.
 */
function buildDynamicColumns<T extends Record<string, unknown>>(
  items: T[]
): ColumnsType<T> {
  if (items.length === 0) return [];
  const keySet = new Set<string>();
  items.forEach((item) => Object.keys(item).forEach((k) => keySet.add(k)));

  return Array.from(keySet).map((key) => ({
    title: key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase()),
    dataIndex: key,
    key,
    render: (value: unknown) => {
      if (value == null) return '-';
      if (typeof value === 'number') return value.toFixed(4);
      return String(value);
    },
  }));
}

const ExecutionDetail: React.FC = () => {
  const { id } = useParams<{ id: string }>();

  const {
    data: execution,
    isLoading,
    error,
  } = useQuery<ExecutionDetailResp>({
    queryKey: ['execution', id],
    queryFn: () => executionApi.getExecution(Number(id)),
    enabled: !!id,
  });

  const duration = useMemo(
    () => formatDuration(execution?.duration),
    [execution?.duration]
  );

  const state = execution?.state ?? 'Initial';
  const cfg = stateConfig[state] ?? stateConfig.Initial;

  const detectorColumns = useMemo(
    () =>
      buildDynamicColumns(
        (execution?.detector_results ?? []) as Array<
          DetectorResultItem & Record<string, unknown>
        >
      ),
    [execution?.detector_results]
  );

  const granularityColumns = useMemo(
    () =>
      buildDynamicColumns(
        (execution?.granularity_results ?? []) as Array<
          GranularityResultItem & Record<string, unknown>
        >
      ),
    [execution?.granularity_results]
  );

  if (isLoading) {
    return (
      <div style={{ padding: 24 }}>
        <Skeleton active paragraph={{ rows: 8 }} />
      </div>
    );
  }

  if (error || !execution) {
    return (
      <div style={{ padding: 24 }}>
        <Card>
          <Empty description='Failed to load execution details.' />
        </Card>
      </div>
    );
  }

  return (
    <div style={{ padding: 24 }}>
      {/* Header */}
      <Space align='center' style={{ marginBottom: 24 }}>
        <Title level={3} style={{ margin: 0 }}>
          {execution.algorithm_name ?? 'Unknown Algorithm'}
        </Title>
        {execution.algorithm_version && (
          <Tag color='blue'>v{execution.algorithm_version}</Tag>
        )}
        <Badge
          status={
            cfg.color === 'success'
              ? 'success'
              : cfg.color === 'error'
                ? 'error'
                : 'default'
          }
          text={
            <Tag icon={cfg.icon} color={cfg.color}>
              {cfg.label}
            </Tag>
          }
        />
        <Text type='secondary'>
          <ClockCircleOutlined style={{ marginRight: 4 }} />
          {duration}
        </Text>
      </Space>

      {/* Info */}
      <Card title='Information' style={{ marginBottom: 24 }}>
        <Descriptions column={{ xs: 1, sm: 2, md: 3 }}>
          {execution.datapack_name != null && (
            <Descriptions.Item label='Datapack'>
              {execution.datapack_id ? (
                <Link to={`/datapacks/${execution.datapack_id}`}>
                  {execution.datapack_name}
                </Link>
              ) : (
                execution.datapack_name
              )}
            </Descriptions.Item>
          )}
          <Descriptions.Item label='Execution ID'>
            {execution.id}
          </Descriptions.Item>
          <Descriptions.Item label='Created'>
            {execution.created_at
              ? dayjs(execution.created_at).format('YYYY-MM-DD HH:mm:ss')
              : '-'}
          </Descriptions.Item>
          {execution.updated_at && (
            <Descriptions.Item label='Updated'>
              {dayjs(execution.updated_at).format('YYYY-MM-DD HH:mm:ss')}
            </Descriptions.Item>
          )}
          {execution.task_id && (
            <Descriptions.Item label='Task ID'>
              <Text code>{execution.task_id}</Text>
            </Descriptions.Item>
          )}
        </Descriptions>
      </Card>

      {/* Detector Results */}
      <Card title='Detector Results' style={{ marginBottom: 24 }}>
        {(execution.detector_results ?? []).length === 0 ? (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description='No detector results yet'
          />
        ) : (
          <Table<DetectorResultItem>
            columns={detectorColumns as ColumnsType<DetectorResultItem>}
            dataSource={execution.detector_results}
            rowKey={(_, index) => String(index)}
            pagination={false}
            scroll={{ x: 'max-content' }}
            size='small'
          />
        )}
      </Card>

      {/* Granularity Results */}
      <Card title='Granularity Results' style={{ marginBottom: 24 }}>
        {(execution.granularity_results ?? []).length === 0 ? (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description='No granularity results yet'
          />
        ) : (
          <Table<GranularityResultItem>
            columns={granularityColumns as ColumnsType<GranularityResultItem>}
            dataSource={execution.granularity_results}
            rowKey={(_, index) => String(index)}
            pagination={false}
            scroll={{ x: 'max-content' }}
            size='small'
          />
        )}
      </Card>

      {/* Task Logs */}
      {execution.task_id && (
        <Card title='Task Logs'>
          <TaskLogViewer taskId={execution.task_id} />
        </Card>
      )}
    </div>
  );
};

export default ExecutionDetail;
