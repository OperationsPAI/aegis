import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { DeleteOutlined, PlusOutlined } from '@ant-design/icons';
import type { ContainerResp, ContainerType } from '@rcabench/client';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Button,
  Card,
  Empty,
  message,
  Select,
  Space,
  Table,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';

import { containerApi } from '@/api/containers';
import { createdAtColumn } from '@/components/ui/columns/createdAtColumn';
import { usePagination } from '@/hooks/usePagination';

const { Title, Text } = Typography;

const ContainerList = () => {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const {
    current: page,
    pageSize: size,
    onChange: onPageChange,
  } = usePagination({ defaultPageSize: 10 });
  const [typeFilter, setTypeFilter] = useState<string | undefined>();
  const [selectedRowKeys, setSelectedRowKeys] = useState<React.Key[]>([]);
  const [batchDeleting, setBatchDeleting] = useState(false);

  // Fetch containers
  const { data, isLoading } = useQuery({
    queryKey: ['containers', { page, size, type: typeFilter }],
    queryFn: () =>
      containerApi.getContainers({
        page,
        size,
        type: typeFilter as ContainerType | undefined,
      }),
  });

  // Delete mutation
  const deleteMutation = useMutation({
    mutationFn: (id: number) => containerApi.deleteContainer(id),
    onSuccess: () => {
      message.success('Container deleted successfully');
      queryClient.invalidateQueries({ queryKey: ['containers'] });
    },
    onError: () => {
      message.error('Failed to delete container');
    },
  });

  const handleBatchDelete = async () => {
    if (selectedRowKeys.length === 0) return;
    setBatchDeleting(true);
    try {
      await Promise.all(
        selectedRowKeys.map((id) => containerApi.deleteContainer(id as number))
      );
      message.success(`Deleted ${selectedRowKeys.length} container(s)`);
      setSelectedRowKeys([]);
      queryClient.invalidateQueries({ queryKey: ['containers'] });
    } catch {
      message.error('Failed to delete some containers');
    } finally {
      setBatchDeleting(false);
    }
  };

  const columns: ColumnsType<ContainerResp> = [
    {
      title: 'Container Name',
      dataIndex: 'name',
      key: 'name',
      render: (text: string) => (
        <Typography.Text strong style={{ color: 'var(--color-primary-600)' }}>
          {text}
        </Typography.Text>
      ),
    },
    {
      title: 'Type',
      dataIndex: 'type',
      key: 'type',
      width: 120,
      render: (type: string) => {
        const colorMap: Record<string, string> = {
          Pedestal: 'blue',
          Benchmark: 'green',
          Algorithm: 'purple',
        };
        return <Tag color={colorMap[type] || 'default'}>{type}</Tag>;
      },
    },
    {
      title: 'Visibility',
      dataIndex: 'is_public',
      key: 'is_public',
      width: 100,
      render: (isPublic: boolean) => (
        <Tag color={isPublic ? 'green' : 'orange'}>
          {isPublic ? 'Public' : 'Private'}
        </Tag>
      ),
    },
    {
      title: 'Status',
      dataIndex: 'status',
      key: 'status',
      width: 100,
      render: (status: string) => (
        <Tag color={status === 'active' ? 'green' : 'default'}>
          {status || '-'}
        </Tag>
      ),
    },
    createdAtColumn<ContainerResp>(),
    {
      title: 'Actions',
      key: 'actions',
      width: 150,
      render: (_, record) => (
        <Space>
          <Button
            type='link'
            size='small'
            onClick={() => navigate(`/admin/containers/${record.id}`)}
          >
            View
          </Button>
          <Button
            type='link'
            size='small'
            onClick={() => navigate(`/admin/containers/${record.id}/edit`)}
          >
            Edit
          </Button>
          <Button
            type='link'
            size='small'
            danger
            icon={<DeleteOutlined />}
            loading={deleteMutation.isPending}
            onClick={() =>
              record.id !== undefined && deleteMutation.mutate(record.id)
            }
          />
        </Space>
      ),
    },
  ];

  return (
    <div className='container-list page-container'>
      <div
        className='page-header'
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: '24px',
        }}
      >
        <Title level={4} className='page-title' style={{ margin: 0 }}>
          Containers
        </Title>
        <Space>
          <Select
            placeholder='Container Type'
            style={{ width: 150 }}
            allowClear
            value={typeFilter}
            onChange={(value) => setTypeFilter(value)}
            options={[
              { label: 'Pedestal', value: 'Pedestal' },
              { label: 'Benchmark', value: 'Benchmark' },
              { label: 'Algorithm', value: 'Algorithm' },
            ]}
          />
          <Button
            type='primary'
            icon={<PlusOutlined />}
            onClick={() => navigate('/admin/containers/new')}
          >
            Create Container
          </Button>
        </Space>
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

      <Card className='table-card'>
        <Table
          columns={columns}
          dataSource={data?.items || []}
          rowKey='id'
          loading={isLoading}
          className='containers-table'
          locale={{ emptyText: <Empty description='No containers yet' /> }}
          rowSelection={{
            selectedRowKeys,
            onChange: setSelectedRowKeys,
          }}
          pagination={{
            current: page,
            pageSize: size,
            total: data?.pagination?.total || 0,
            showSizeChanger: true,
            showQuickJumper: true,
            showTotal: (total) => `Total ${total} containers`,
            onChange: onPageChange,
          }}
        />
      </Card>
    </div>
  );
};

export default ContainerList;
