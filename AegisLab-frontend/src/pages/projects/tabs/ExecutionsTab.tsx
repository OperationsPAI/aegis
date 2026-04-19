import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { PlayCircleOutlined } from '@ant-design/icons';
import type { ExecutionResp } from '@rcabench/client';
import { useQuery } from '@tanstack/react-query';
import { Button, Table, Tag } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import dayjs from 'dayjs';

import { projectApi } from '@/api/projects';

interface ExecutionsTabProps {
  projectId: number;
}

const executionStateMap: Record<number, { label: string; color: string }> = {
  0: { label: 'Initial', color: 'default' },
  1: { label: 'Failed', color: 'red' },
  2: { label: 'Success', color: 'green' },
};

/**
 * Executions listing tab with state badges and navigation.
 */
const ExecutionsTab: React.FC<ExecutionsTabProps> = ({ projectId }) => {
  const navigate = useNavigate();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);

  const { data, isLoading } = useQuery({
    queryKey: ['project', projectId, 'executions', page, pageSize],
    queryFn: () =>
      projectApi.getExecutions(projectId, { page, size: pageSize }),
  });

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
    <div>
      <div style={{ marginBottom: 16 }}>
        <Button
          type='primary'
          icon={<PlayCircleOutlined />}
          onClick={() => navigate(`/projects/${projectId}/execute`)}
        >
          Run Algorithm
        </Button>
      </div>

      <Table
        columns={columns}
        dataSource={items}
        rowKey='id'
        loading={isLoading}
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

export default ExecutionsTab;
