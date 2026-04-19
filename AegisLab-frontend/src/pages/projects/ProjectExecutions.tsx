import { useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { DeleteOutlined, PlayCircleOutlined } from '@ant-design/icons';
import type { ExecutionResp } from '@rcabench/client';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Button,
  Card,
  Empty,
  message,
  Skeleton,
  Space,
  Table,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import dayjs from 'dayjs';

import { executionApi } from '@/api/executions';
import { projectApi } from '@/api/projects';

import ProjectSubNav from './ProjectSubNav';
import { executionStateMap } from './stateLabels';


/**
 * Full executions listing page for a project.
 * Route: /projects/:id/executions
 */
const { Text } = Typography;

const ProjectExecutions: React.FC = () => {
  const { id } = useParams<{ id: string }>();
  const projectId = Number(id);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const [selectedRowKeys, setSelectedRowKeys] = useState<React.Key[]>([]);
  const [batchDeleting, setBatchDeleting] = useState(false);

  const { isLoading: projectLoading } = useQuery({
    queryKey: ['project', projectId],
    queryFn: () => projectApi.getProjectDetail(projectId),
    enabled: !!projectId && !Number.isNaN(projectId),
  });

  const { data, isLoading } = useQuery({
    queryKey: ['project', projectId, 'executions', page, pageSize],
    queryFn: () =>
      projectApi.getExecutions(projectId, { page, size: pageSize }),
    enabled: !!projectId && !Number.isNaN(projectId),
  });

  const handleBatchDelete = async () => {
    if (selectedRowKeys.length === 0) return;
    setBatchDeleting(true);
    try {
      await executionApi.batchDelete(selectedRowKeys as number[]);
      message.success(`Deleted ${selectedRowKeys.length} execution(s)`);
      setSelectedRowKeys([]);
      queryClient.invalidateQueries({
        queryKey: ['project', projectId, 'executions'],
      });
    } catch {
      message.error('Failed to delete executions');
    } finally {
      setBatchDeleting(false);
    }
  };

  if (projectLoading) {
    return (
      <div style={{ padding: 24 }}>
        <Skeleton active paragraph={{ rows: 6 }} />
      </div>
    );
  }

  const columns: ColumnsType<ExecutionResp> = [
    {
      title: 'Algorithm',
      dataIndex: 'algorithm_name',
      key: 'algorithm_name',
      render: (name: string, record) => (
        <span>
          {name ?? '-'}
          {record.algorithm_version ? ` (${record.algorithm_version})` : ''}
        </span>
      ),
    },
    {
      title: 'Datapack',
      dataIndex: 'datapack_name',
      key: 'datapack_name',
      render: (name: string) => name ?? '-',
    },
    {
      title: 'State',
      dataIndex: 'state',
      key: 'state',
      render: (state: number) => {
        const mapping = executionStateMap[state] ?? {
          label: 'Unknown',
          color: 'default',
        };
        return <Tag color={mapping.color}>{mapping.label}</Tag>;
      },
    },
    {
      title: 'Duration',
      dataIndex: 'duration',
      key: 'duration',
      render: (duration: number | undefined) => {
        if (duration == null) return '-';
        if (duration < 60) return `${duration}s`;
        const minutes = Math.floor(duration / 60);
        const seconds = duration % 60;
        return `${minutes}m ${seconds}s`;
      },
    },
    {
      title: 'Created',
      dataIndex: 'created_at',
      key: 'created_at',
      render: (date: string) =>
        date ? dayjs(date).format('YYYY-MM-DD HH:mm') : '-',
    },
  ];

  const items = data?.items ?? [];
  const total = data?.pagination?.total ?? 0;

  return (
    <div style={{ padding: 24 }}>
      <ProjectSubNav projectId={projectId} activeKey='executions' />

      <div style={{ marginBottom: 16 }}>
        <Button
          type='primary'
          icon={<PlayCircleOutlined />}
          onClick={() => navigate(`/projects/${projectId}/execute`)}
        >
          Run Algorithm
        </Button>
      </div>

      {selectedRowKeys.length > 0 && (
        <Card size='small' style={{ marginBottom: 16 }}>
          <Space>
            <Text>{selectedRowKeys.length} selected</Text>
            <Button
              danger
              icon={<DeleteOutlined />}
              loading={batchDeleting}
              onClick={handleBatchDelete}
            >
              Delete Selected
            </Button>
          </Space>
        </Card>
      )}

      <Table
        columns={columns}
        dataSource={items}
        rowKey='id'
        loading={isLoading}
        locale={{ emptyText: <Empty description='No executions yet' /> }}
        rowSelection={{
          selectedRowKeys,
          onChange: setSelectedRowKeys,
        }}
        onRow={(record) => ({
          onClick: () => navigate(`/executions/${record.id}`),
          style: { cursor: 'pointer' },
        })}
        pagination={{
          current: page,
          pageSize,
          total,
          onChange: (p, s) => {
            setPage(p);
            setPageSize(s);
          },
          showSizeChanger: true,
          showTotal: (t, range) => `${range[0]}-${range[1]} of ${t}`,
        }}
      />
    </div>
  );
};

export default ProjectExecutions;
