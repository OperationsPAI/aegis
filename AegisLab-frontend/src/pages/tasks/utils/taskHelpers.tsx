import {
  CheckCircleOutlined,
  ClockCircleOutlined,
  CloseCircleOutlined,
  DashboardOutlined,
  DatabaseOutlined,
  FunctionOutlined,
  PauseCircleOutlined,
  ReloadOutlined,
  SyncOutlined,
} from '@ant-design/icons';
import { TaskState, TaskType } from '@rcabench/client';

/** Convert a string state to TaskState enum. */
export const getTaskState = (state?: string): TaskState => {
  const numState = parseInt(state || '0', 10);
  if (Object.values(TaskState).includes(numState as TaskState)) {
    return numState as TaskState;
  }
  return TaskState.Pending;
};

/** Convert a string type to TaskType enum. */
export const getTaskType = (type?: string): TaskType => {
  const numType = parseInt(type || '0', 10);
  if (Object.values(TaskType).includes(numType as TaskType)) {
    return numType as TaskType;
  }
  return TaskType.BuildContainer;
};

export const getTaskTypeIcon = (type: TaskType) => {
  switch (type) {
    case TaskType.BuildContainer:
      return <DashboardOutlined style={{ color: 'var(--color-success)' }} />;
    case TaskType.RestartPedestal:
      return <ReloadOutlined style={{ color: 'var(--color-primary-500)' }} />;
    case TaskType.FaultInjection:
      return <SyncOutlined style={{ color: 'var(--color-warning)' }} />;
    case TaskType.RunAlgorithm:
      return <FunctionOutlined style={{ color: 'var(--color-info)' }} />;
    case TaskType.BuildDatapack:
      return <DatabaseOutlined style={{ color: 'var(--color-primary-700)' }} />;
    case TaskType.CollectResult:
      return <DatabaseOutlined style={{ color: 'var(--color-primary-700)' }} />;
    case TaskType.CronJob:
      return (
        <ClockCircleOutlined style={{ color: 'var(--color-secondary-500)' }} />
      );
    default:
      return <ClockCircleOutlined />;
  }
};

export const getTaskTypeColor = (type: TaskType) => {
  switch (type) {
    case TaskType.BuildContainer:
      return 'var(--color-success)';
    case TaskType.RestartPedestal:
      return 'var(--color-primary-500)';
    case TaskType.FaultInjection:
      return 'var(--color-warning)';
    case TaskType.RunAlgorithm:
      return 'var(--color-info)';
    case TaskType.BuildDatapack:
      return 'var(--color-primary-700)';
    case TaskType.CollectResult:
      return 'var(--color-primary-700)';
    case TaskType.CronJob:
      return 'var(--color-secondary-500)';
    default:
      return 'var(--color-secondary-500)';
  }
};

export const getStateColor = (state: TaskState) => {
  switch (state) {
    case TaskState.Pending:
      return 'var(--color-secondary-300)';
    case TaskState.Rescheduled:
      return 'var(--color-secondary-400)';
    case TaskState.Running:
      return 'var(--color-primary-500)';
    case TaskState.Completed:
      return 'var(--color-success)';
    case TaskState.Error:
      return 'var(--color-error)';
    case TaskState.Cancelled:
      return 'var(--color-secondary-500)';
    default:
      return 'var(--color-secondary-500)';
  }
};

export const getStateIcon = (state: TaskState) => {
  switch (state) {
    case TaskState.Pending:
      return <ClockCircleOutlined />;
    case TaskState.Rescheduled:
      return (
        <ClockCircleOutlined style={{ color: 'var(--color-secondary-400)' }} />
      );
    case TaskState.Running:
      return <SyncOutlined spin />;
    case TaskState.Completed:
      return <CheckCircleOutlined />;
    case TaskState.Error:
      return <CloseCircleOutlined />;
    case TaskState.Cancelled:
      return <PauseCircleOutlined />;
    default:
      return <ClockCircleOutlined />;
  }
};

export const getStateName = (state: TaskState): string => {
  switch (state) {
    case TaskState.Pending:
      return 'Pending';
    case TaskState.Rescheduled:
      return 'Rescheduled';
    case TaskState.Running:
      return 'Running';
    case TaskState.Completed:
      return 'Completed';
    case TaskState.Error:
      return 'Error';
    case TaskState.Cancelled:
      return 'Cancelled';
    default:
      return 'Unknown';
  }
};

export const getStateBadgeStatus = (
  state: TaskState
): 'success' | 'processing' | 'error' | 'default' | 'warning' => {
  switch (state) {
    case TaskState.Completed:
      return 'success';
    case TaskState.Error:
      return 'error';
    case TaskState.Running:
      return 'processing';
    case TaskState.Cancelled:
      return 'warning';
    default:
      return 'default';
  }
};

export const getStatusBadgeKey = (
  state: TaskState
): 'completed' | 'error' | 'running' | 'warning' | 'pending' => {
  switch (state) {
    case TaskState.Completed:
      return 'completed';
    case TaskState.Error:
      return 'error';
    case TaskState.Running:
      return 'running';
    case TaskState.Cancelled:
      return 'warning';
    default:
      return 'pending';
  }
};
