import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { ReloadOutlined, SyncOutlined } from '@ant-design/icons';
import {
  type ListTasksTaskType,
  type TaskResp,
  TaskState,
} from '@rcabench/client';
import { useQuery } from '@tanstack/react-query';
import {
  Badge,
  Button,
  Card,
  Empty,
  message,
  Modal,
  Space,
  Table,
  type TablePaginationConfig,
  Typography,
} from 'antd';

import { taskApi } from '@/api/tasks';
import { usePagination } from '@/hooks/usePagination';

import TaskFilters from './components/TaskFilters';
import TaskStats from './components/TaskStats';
import { useTasksSSE } from './hooks/useTasksSSE';

import { buildTaskColumns } from './taskColumns';

const { Title, Text } = Typography;

const TaskList = () => {
  const navigate = useNavigate();
  const [typeFilter, setTypeFilter] = useState<ListTasksTaskType | undefined>();
  const [stateFilter, setStateFilter] = useState<TaskState | undefined>();
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [refreshInterval] = useState(5000);
  const {
    current,
    pageSize,
    onChange: onPaginationChange,
    reset: resetPagination,
  } = usePagination({ defaultPageSize: 10 });

  const {
    data: tasksData,
    isLoading,
    refetch,
  } = useQuery({
    queryKey: ['tasks', current, pageSize, typeFilter, stateFilter],
    queryFn: () =>
      taskApi.getTasks({
        page: current,
        size: pageSize,
        taskType: typeFilter as string | undefined,
        state: stateFilter,
      }),
    refetchInterval: autoRefresh ? refreshInterval : false,
  });

  // Real-time SSE updates for running tasks
  useTasksSSE(autoRefresh, tasksData?.items, refetch);

  // Statistics
  const stats = {
    total: tasksData?.pagination?.total || 0,
    pending:
      tasksData?.items?.filter(
        (t: TaskResp) => String(t.state) === String(TaskState.Pending)
      ).length || 0,
    running:
      tasksData?.items?.filter(
        (t: TaskResp) => String(t.state) === String(TaskState.Running)
      ).length || 0,
    completed:
      tasksData?.items?.filter(
        (t: TaskResp) => String(t.state) === String(TaskState.Completed)
      ).length || 0,
    error:
      tasksData?.items?.filter(
        (t: TaskResp) => String(t.state) === String(TaskState.Error)
      ).length || 0,
    cancelled:
      tasksData?.items?.filter(
        (t: TaskResp) => String(t.state) === String(TaskState.Cancelled)
      ).length || 0,
  };

  const handleTableChange = (newPagination: TablePaginationConfig) => {
    onPaginationChange(
      newPagination.current || 1,
      newPagination.pageSize || 10
    );
  };

  const handleSearch = (_value: string) => {
    resetPagination();
  };

  const handleTypeFilter = (type: ListTasksTaskType | undefined) => {
    setTypeFilter(type);
    resetPagination();
  };

  const handleStateFilter = (state: TaskState | undefined) => {
    setStateFilter(state);
    resetPagination();
  };

  const handleViewTask = (id?: string) => {
    if (id) navigate(`/tasks/${id}`);
  };

  const handleDeleteTask = (id?: string) => {
    if (!id) return;
    Modal.confirm({
      title: 'Delete Task',
      content:
        'Are you sure you want to delete this task? This action cannot be undone.',
      okText: 'Yes, delete it',
      okButtonProps: { danger: true },
      cancelText: 'Cancel',
      onOk: async () => {
        try {
          await taskApi.batchDelete([id]);
          message.success('Task deleted successfully');
          refetch();
        } catch {
          message.error('Failed to delete task');
        }
      },
    });
  };

  const handleManualRefresh = () => {
    refetch();
    message.success('Tasks refreshed');
  };

  const columns = buildTaskColumns(handleViewTask, handleDeleteTask);

  return (
    <div className='task-list page-container'>
      {/* Page Header */}
      <div className='page-header'>
        <div className='page-header-left'>
          <Title level={4} className='page-title'>
            Task Monitor
          </Title>
          <Text type='secondary'>
            Monitor and manage background tasks with real-time updates
          </Text>
        </div>
        <div className='page-header-right'>
          <Space>
            <Button icon={<ReloadOutlined />} onClick={handleManualRefresh}>
              Refresh
            </Button>
            <Button
              type={autoRefresh ? 'primary' : 'default'}
              icon={<SyncOutlined spin={autoRefresh} />}
              onClick={() => setAutoRefresh(!autoRefresh)}
            >
              {autoRefresh ? 'Auto-refresh ON' : 'Auto-refresh OFF'}
            </Button>
          </Space>
        </div>
      </div>

      {/* Statistics */}
      <TaskStats {...stats} />

      {/* Filters */}
      <TaskFilters
        typeFilter={typeFilter}
        stateFilter={stateFilter}
        onSearch={handleSearch}
        onTypeFilter={handleTypeFilter}
        onStateFilter={handleStateFilter}
      />

      {/* Task Table */}
      <Card className='table-card'>
        <Table
          rowKey='id'
          columns={columns}
          dataSource={(tasksData?.items as TaskResp[] | undefined) || []}
          loading={isLoading}
          className='tasks-table'
          pagination={{
            current,
            pageSize,
            total: tasksData?.pagination?.total || 0,
            showSizeChanger: true,
            showQuickJumper: true,
            showTotal: (total, range) =>
              `${range[0]}-${range[1]} of ${total} tasks`,
          }}
          onChange={handleTableChange}
          onRow={(record) => ({
            onClick: () => handleViewTask(record.id),
            style: { cursor: 'pointer' },
          })}
          locale={{
            emptyText: <Empty description='No tasks found' />,
          }}
        />
      </Card>

      {/* Real-time Status Indicator */}
      {autoRefresh && (
        <div style={{ position: 'fixed', bottom: 24, right: 24 }}>
          <Card size='small' style={{ width: 200 }}>
            <Space>
              <Badge status='processing' />
              <Text type='secondary'>Real-time updates active</Text>
            </Space>
          </Card>
        </div>
      )}
    </div>
  );
};

export default TaskList;
