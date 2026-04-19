import { Link } from 'react-router-dom';

import { DeleteOutlined, EyeOutlined } from '@ant-design/icons';
import { ListTasksTaskType, type TaskResp, TaskState } from '@rcabench/client';
import { Badge, Button, Space, Tag, Tooltip, Typography } from 'antd';
import dayjs from 'dayjs';

import {
  getStateBadgeStatus,
  getStateColor,
  getStateIcon,
  getStateName,
} from './utils/taskHelpers';

const { Text } = Typography;

/** Human-readable task type names */
const taskTypeNames: Record<string, string> = {
  '0': 'Build Container',
  '1': 'Restart Pedestal',
  '2': 'Fault Injection',
  '3': 'Run Algorithm',
  '4': 'Build Datapack',
  '5': 'Collect Result',
  '6': 'Cron Job',
};

const getTaskTypeName = (
  type: ListTasksTaskType | string | undefined
): string => {
  if (type === undefined || type === null) return 'Unknown';
  return taskTypeNames[String(type)] ?? String(type);
};

const getTaskTypeColor = (
  type: ListTasksTaskType | string | undefined
): string => {
  if (type === undefined || type === null) return 'var(--color-secondary-500)';

  const typeStr = String(type);
  switch (typeStr) {
    case '0':
      return 'var(--color-primary-500)';
    case '1':
      return 'var(--color-success)';
    case '2':
      return 'var(--color-warning)';
    case '3':
      return 'var(--color-info)';
    case '4':
      return 'var(--color-success)';
    case '5':
      return 'var(--color-primary-700)';
    case '6':
      return 'var(--color-secondary-500)';
    default:
      return 'var(--color-secondary-500)';
  }
};

export function buildTaskColumns(
  onView: (id?: string) => void,
  onDelete: (id?: string) => void
) {
  return [
    {
      title: 'ID',
      dataIndex: 'id',
      key: 'id',
      width: '10%',
      render: (id: string) => (
        <Text strong style={{ fontSize: '0.875rem' }}>
          {id?.substring(0, 8)}
        </Text>
      ),
    },
    {
      title: 'Type',
      dataIndex: 'type',
      key: 'type',
      width: '14%',
      render: (type: ListTasksTaskType | string | undefined) => (
        <Tag color={getTaskTypeColor(type)} style={{ fontWeight: 500 }}>
          {getTaskTypeName(type)}
        </Tag>
      ),
      filters: [
        { text: 'Build Container', value: ListTasksTaskType.NUMBER_0 },
        { text: 'Restart Pedestal', value: ListTasksTaskType.NUMBER_1 },
        { text: 'Fault Injection', value: ListTasksTaskType.NUMBER_2 },
        { text: 'Run Algorithm', value: ListTasksTaskType.NUMBER_3 },
        { text: 'Build Datapack', value: ListTasksTaskType.NUMBER_4 },
        { text: 'Collect Result', value: ListTasksTaskType.NUMBER_5 },
        { text: 'Cron Job', value: ListTasksTaskType.NUMBER_6 },
      ],
      onFilter: (value: boolean | React.Key, record: TaskResp) =>
        record.type === value || String(record.type) === String(value),
    },
    {
      title: 'Project',
      key: 'project',
      width: '14%',
      render: (_: unknown, record: TaskResp) => {
        const projectId = record.project_id;
        const projectName = record.project_name;
        if (projectId) {
          return (
            <Link
              to={`/projects/${projectId}`}
              onClick={(e) => e.stopPropagation()}
            >
              {projectName || String(projectId).substring(0, 8)}
            </Link>
          );
        }
        return (
          <Text type='secondary' style={{ fontSize: '0.75rem' }}>
            -
          </Text>
        );
      },
    },
    {
      title: 'State',
      dataIndex: 'state',
      key: 'state',
      width: '12%',
      render: (state: string | TaskState) => {
        const stateStr = String(state);
        let taskState: TaskState;
        if (!isNaN(Number(stateStr))) {
          taskState = Number(stateStr) as TaskState;
        } else {
          taskState = TaskState.Pending;
        }

        return (
          <Badge
            status={getStateBadgeStatus(taskState)}
            text={
              <Space size='small'>
                {getStateIcon(taskState)}
                <Text
                  strong
                  style={{
                    color: getStateColor(taskState),
                    fontSize: '0.875rem',
                  }}
                >
                  {getStateName(taskState)}
                </Text>
              </Space>
            }
          />
        );
      },
      filters: [
        { text: 'Pending', value: TaskState.Pending },
        { text: 'Rescheduled', value: TaskState.Rescheduled },
        { text: 'Running', value: TaskState.Running },
        { text: 'Completed', value: TaskState.Completed },
        { text: 'Error', value: TaskState.Error },
        { text: 'Cancelled', value: TaskState.Cancelled },
      ],
      onFilter: (value: boolean | React.Key, record: TaskResp) =>
        String(record.state) === String(value),
    },
    {
      title: 'Created',
      dataIndex: 'created_at',
      key: 'created_at',
      width: '12%',
      render: (date: string) => (
        <Tooltip title={dayjs(date).format('YYYY-MM-DD HH:mm:ss')}>
          <Text style={{ fontSize: '0.75rem' }}>{dayjs(date).fromNow()}</Text>
        </Tooltip>
      ),
    },
    {
      title: 'Duration',
      key: 'duration',
      width: '10%',
      render: (_: unknown, record: TaskResp) => {
        const start = record.created_at;
        const end = record.updated_at;
        if (!start) return <Text type='secondary'>-</Text>;

        const isRunning = String(record.state) === String(TaskState.Running);
        const endTime = isRunning ? dayjs() : dayjs(end || start);
        const diffMs = endTime.diff(dayjs(start));

        if (diffMs < 1000)
          return <Text style={{ fontSize: '0.75rem' }}>&lt;1s</Text>;
        if (diffMs < 60000) {
          return (
            <Text style={{ fontSize: '0.75rem' }}>
              {Math.round(diffMs / 1000)}s
            </Text>
          );
        }
        const mins = Math.floor(diffMs / 60000);
        const secs = Math.round((diffMs % 60000) / 1000);
        return (
          <Text style={{ fontSize: '0.75rem' }}>
            {mins}m {secs}s
          </Text>
        );
      },
    },
    {
      title: 'Actions',
      key: 'actions',
      width: '8%',
      render: (_: unknown, record: TaskResp) => (
        <Space size='small'>
          <Tooltip title='View Details'>
            <Button
              type='text'
              size='small'
              icon={<EyeOutlined />}
              onClick={(e) => {
                e.stopPropagation();
                onView(record.id);
              }}
            />
          </Tooltip>
          <Tooltip title='Delete Task'>
            <Button
              type='text'
              size='small'
              danger
              icon={<DeleteOutlined />}
              onClick={(e) => {
                e.stopPropagation();
                onDelete(record.id);
              }}
            />
          </Tooltip>
        </Space>
      ),
    },
  ];
}
