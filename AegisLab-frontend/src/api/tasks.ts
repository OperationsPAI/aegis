import type {
  ListTaskResp,
  StatusType,
  TaskDetailResp,
  TaskState,
} from '@rcabench/client';

import { getAccessToken } from '@/utils/authToken';

import apiClient from './client';

export const taskApi = {
  getTasks: (params?: {
    page?: number;
    size?: number;
    taskType?: string;
    immediate?: boolean;
    traceId?: string;
    groupId?: string;
    projectId?: number;
    state?: TaskState;
    status?: StatusType;
  }): Promise<ListTaskResp | undefined> =>
    apiClient.get('/tasks', { params }).then((r) => r.data.data),

  getTask: (taskId: string): Promise<TaskDetailResp | undefined> =>
    apiClient.get(`/tasks/${taskId}`).then((r) => r.data.data),

  batchDelete: (ids: string[]) =>
    apiClient.post('/tasks/batch-delete', { ids }).then((r) => r.data),
};

/**
 * Create WebSocket connection for task logs
 * Backend endpoint: GET /tasks/:task_id/logs/ws
 */
export const createTaskLogWebSocket = (taskId: string): WebSocket => {
  const token = getAccessToken();
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const url = `${protocol}//${window.location.host}/api/v2/tasks/${taskId}/logs/ws${token ? `?token=${encodeURIComponent(token)}` : ''}`;
  return new WebSocket(url);
};
