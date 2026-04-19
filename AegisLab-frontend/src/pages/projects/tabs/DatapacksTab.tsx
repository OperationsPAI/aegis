import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { PlusOutlined } from '@ant-design/icons';
import type { InjectionResp } from '@rcabench/client';
import { useQuery } from '@tanstack/react-query';
import { Button, Space, Table, Tag } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import dayjs from 'dayjs';

import { projectApi } from '@/api/projects';

interface DatapacksTabProps {
  projectId: number;
}

const injectionStateMap: Record<number, { label: string; color: string }> = {
  0: { label: 'Initial', color: 'default' },
  1: { label: 'Inject Failed', color: 'red' },
  2: { label: 'Inject Success', color: 'blue' },
  3: { label: 'Build Failed', color: 'red' },
  4: { label: 'Build Success', color: 'green' },
  5: { label: 'Detector Failed', color: 'red' },
  6: { label: 'Detector Success', color: 'green' },
};

/**
 * Datapacks listing tab with injection state badges and navigation.
 */
const DatapacksTab: React.FC<DatapacksTabProps> = ({ projectId }) => {
  const navigate = useNavigate();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);

  const { data, isLoading } = useQuery({
    queryKey: ['project', projectId, 'injections', page, pageSize],
    queryFn: () =>
      projectApi.listProjectInjections(projectId, { page, size: pageSize }),
  });

  const columns: ColumnsType<InjectionResp> = [
    {
      title: 'Name',
      dataIndex: 'name',
      key: 'name',
    },
    {
      title: 'Fault Type',
      dataIndex: 'fault_type',
      key: 'fault_type',
    },
    {
      title: 'State',
      dataIndex: 'state',
      key: 'state',
      render: (state: number) => {
        const mapping = injectionStateMap[state] ?? {
          label: 'Unknown',
          color: 'default',
        };
        return <Tag color={mapping.color}>{mapping.label}</Tag>;
      },
    },
    {
      title: 'Benchmark',
      dataIndex: 'benchmark_name',
      key: 'benchmark_name',
    },
    {
      title: 'Pedestal',
      dataIndex: 'pedestal_name',
      key: 'pedestal_name',
    },
    {
      title: 'Created',
      dataIndex: 'created_at',
      key: 'created_at',
      render: (date: string) =>
        date ? dayjs(date).format('YYYY-MM-DD HH:mm') : '-',
    },
  ];

  return (
    <div>
      <Space style={{ marginBottom: 16 }}>
        <Button
          type='primary'
          icon={<PlusOutlined />}
          onClick={() => navigate(`/projects/${projectId}/inject`)}
        >
          New Injection
        </Button>
      </Space>

      <Table
        columns={columns}
        dataSource={data?.items ?? []}
        rowKey='id'
        loading={isLoading}
        onRow={(record) => ({
          onClick: () => navigate(`/datapacks/${record.id}`),
          style: { cursor: 'pointer' },
        })}
        pagination={{
          current: page,
          pageSize,
          total: data?.total ?? 0,
          onChange: (p, s) => {
            setPage(p);
            setPageSize(s);
          },
          showSizeChanger: true,
          showTotal: (total, range) => `${range[0]}-${range[1]} of ${total}`,
        }}
      />
    </div>
  );
};

export default DatapacksTab;
