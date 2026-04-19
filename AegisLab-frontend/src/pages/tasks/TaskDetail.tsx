import { useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import {
  ArrowLeftOutlined,
  CopyOutlined,
  DownloadOutlined,
  PauseCircleOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import { type TaskDetailResp, TaskState } from '@rcabench/client';
import { useQuery } from '@tanstack/react-query';
import {
  Badge,
  Button,
  Card,
  Col,
  message,
  Modal,
  Row,
  Space,
  Switch,
  Tabs,
  Typography,
} from 'antd';

import { taskApi } from '@/api/tasks';

import TaskLogsPanel from './components/TaskLogsPanel';
import TaskOverview from './components/TaskOverview';
import TaskProgressCard from './components/TaskProgressCard';
import TaskTimeline from './components/TaskTimeline';
import { useTaskSSE } from './hooks/useTaskSSE';
import {
  getStateBadgeStatus,
  getStateColor,
  getStateIcon,
  getStateName,
  getTaskState,
} from './utils/taskHelpers';

const { Title, Text } = Typography;

const TaskDetail = () => {
  const navigate = useNavigate();
  const { id } = useParams<{ id: string }>();
  const [activeTab, setActiveTab] = useState('overview');
  const [logs, setLogs] = useState<string[]>([]);
  const [autoRefresh, setAutoRefresh] = useState(true);

  const taskId = id;

  const {
    data: task,
    isLoading,
    refetch,
  } = useQuery({
    queryKey: ['task', taskId],
    queryFn: async () => {
      if (!taskId) {
        throw new Error('Task ID is required');
      }
      return taskApi.getTask(taskId);
    },
    refetchInterval: autoRefresh ? 2000 : false,
    enabled: !!taskId,
  });

  useTaskSSE(task, refetch, setLogs);

  const handleCancelTask = () => {
    const taskState = getTaskState(task?.state);
    if (taskState !== TaskState.Running && taskState !== TaskState.Pending) {
      message.warning('Only running or pending tasks can be cancelled');
      return;
    }

    Modal.confirm({
      title: 'Cancel Task',
      content: `Are you sure you want to cancel task "${taskId}"?`,
      okText: 'Yes, cancel it',
      okButtonProps: { danger: true },
      cancelText: 'No',
      onOk: () => {
        message.info('Task cancellation is not yet supported by the backend');
      },
    });
  };

  const handleRetryTask = () => {
    if (getTaskState(task?.state) !== TaskState.Error) {
      message.warning('Only failed tasks can be retried');
      return;
    }

    Modal.confirm({
      title: 'Retry Task',
      content: `Are you sure you want to retry task "${taskId}"?`,
      okText: 'Yes, retry it',
      cancelText: 'No',
      onOk: () => {
        message.info('Task retry is not yet supported by the backend');
      },
    });
  };

  const handleDownloadLogs = () => {
    const logContent = logs.join('\n');
    const blob = new Blob([logContent], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `task-${taskId}-logs.txt`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
    message.success('Logs downloaded successfully');
  };

  if (isLoading) {
    return (
      <div style={{ padding: 24 }}>
        <Card loading>
          <div style={{ minHeight: 400 }} />
        </Card>
      </div>
    );
  }

  if (!taskId) {
    return (
      <div style={{ padding: 24, textAlign: 'center' }}>
        <Text type='secondary'>Task ID not provided</Text>
      </div>
    );
  }

  if (!task) {
    return (
      <div style={{ padding: 24, textAlign: 'center' }}>
        <Text type='secondary'>Task not found</Text>
      </div>
    );
  }

  const taskData: TaskDetailResp | undefined = task;
  const state = getTaskState(taskData?.state);
  const isRunning = state === TaskState.Running;
  const isRescheduled = state === TaskState.Rescheduled;

  return (
    <div style={{ padding: 24 }}>
      {/* Header */}
      <div style={{ marginBottom: 24 }}>
        <Space>
          <Button
            icon={<ArrowLeftOutlined />}
            onClick={() => navigate('/tasks')}
          >
            Back to List
          </Button>
          <Title level={4} style={{ margin: 0 }}>
            Task {taskId.substring(0, 8)}
          </Title>
          <Badge
            status={getStateBadgeStatus(state)}
            text={
              <Space>
                {getStateIcon(state)}
                <Text strong style={{ color: getStateColor(state) }}>
                  {getStateName(state)}
                </Text>
              </Space>
            }
          />
        </Space>
      </div>

      {/* Actions */}
      <Card style={{ marginBottom: 24 }}>
        <Row justify='space-between' align='middle'>
          <Col>
            <Space>
              {(state === TaskState.Running || state === TaskState.Pending) && (
                <Button
                  danger
                  icon={<PauseCircleOutlined />}
                  onClick={handleCancelTask}
                >
                  Cancel Task
                </Button>
              )}
              {state === TaskState.Error && (
                <Button
                  type='primary'
                  icon={<ReloadOutlined />}
                  onClick={handleRetryTask}
                >
                  Retry Task
                </Button>
              )}
              <Button
                icon={<DownloadOutlined />}
                onClick={handleDownloadLogs}
                disabled={logs.length === 0}
              >
                Download Logs
              </Button>
              <Button
                icon={<CopyOutlined />}
                onClick={() => {
                  navigator.clipboard.writeText(taskId);
                  message.success('Task ID copied to clipboard');
                }}
              >
                Copy ID
              </Button>
            </Space>
          </Col>
          <Col>
            <Space>
              <Text type='secondary'>Auto-refresh:</Text>
              <Switch
                checked={autoRefresh}
                onChange={setAutoRefresh}
                checkedChildren='ON'
                unCheckedChildren='OFF'
              />
            </Space>
          </Col>
        </Row>
      </Card>

      {/* Progress */}
      <TaskProgressCard
        state={state}
        isRunning={isRunning}
        isRescheduled={isRescheduled}
      />

      {/* Tabs */}
      <Tabs
        activeKey={activeTab}
        onChange={setActiveTab}
        items={[
          {
            key: 'overview',
            label: 'Overview',
            children: <TaskOverview task={taskData} taskId={taskId} />,
          },
          {
            key: 'logs',
            label: 'Logs',
            children: (
              <TaskLogsPanel
                logs={logs}
                onClear={() => setLogs([])}
                onDownload={handleDownloadLogs}
              />
            ),
          },
          {
            key: 'timeline',
            label: 'Timeline',
            children: <TaskTimeline task={taskData} />,
          },
        ]}
      />
    </div>
  );
};

export default TaskDetail;
