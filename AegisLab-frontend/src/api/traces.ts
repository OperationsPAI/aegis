import type { TraceDetailResp } from '@rcabench/client';

import { getAccessToken } from '@/utils/authToken';

import apiClient from './client';

export const traceApi = {
  getTraces: (params?: { page?: number; size?: number }) =>
    apiClient.get('/traces', { params }).then((r) => r.data.data),

  getTrace: (traceId: string): Promise<TraceDetailResp | undefined> =>
    apiClient.get(`/traces/${traceId}`).then((r) => r.data.data),
};

/**
 * Create SSE connection for trace streaming
 * Backend endpoint: GET /traces/:trace_id/stream
 */
export const createTraceStream = (traceId: string): EventSource => {
  const token = getAccessToken();
  const url = `/api/v2/traces/${traceId}/stream${token ? `?token=${encodeURIComponent(token)}` : ''}`;
  return new EventSource(url);
};
