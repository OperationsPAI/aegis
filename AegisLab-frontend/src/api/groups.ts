import { getAccessToken } from '@/utils/authToken';

import apiClient from './client';

export const groupApi = {
  getGroupStats: (groupId: string) =>
    apiClient.get(`/groups/${groupId}/stats`).then((r) => r.data.data),
};

/**
 * Create SSE connection for group streaming
 * Backend endpoint: GET /groups/:group_id/stream
 */
export const createGroupStream = (groupId: string): EventSource => {
  const token = getAccessToken();
  const url = `/api/v2/groups/${groupId}/stream${token ? `?token=${encodeURIComponent(token)}` : ''}`;
  return new EventSource(url);
};
