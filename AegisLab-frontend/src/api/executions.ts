import type {
  DetectorResultItem,
  ExecutionDetailResp,
  GranularityResultItem,
  LabelItem,
  ListExecutionResp,
} from '@rcabench/client';

import apiClient from './client';

export const executionApi = {
  getExecutions: (params?: {
    page?: number;
    size?: number;
    state?: string;
    status?: string;
    labels?: string[];
  }): Promise<ListExecutionResp> =>
    apiClient
      .get('/executions', { params })
      .then((r) => r.data.data as ListExecutionResp),

  getExecution: (id: number): Promise<ExecutionDetailResp> =>
    apiClient
      .get(`/executions/${id}`)
      .then((r) => r.data.data as ExecutionDetailResp),

  getExecutionLabels: (): Promise<LabelItem[] | undefined> =>
    apiClient.get('/executions/labels').then((r) => r.data.data),

  uploadDetectorResults: (
    id: number,
    data: { duration: number; results: DetectorResultItem[] }
  ) =>
    apiClient
      .post(`/executions/${id}/detector_results`, data)
      .then((r) => r.data.data),

  uploadGranularityResults: (
    id: number,
    data: { duration: number; results: GranularityResultItem[] }
  ) =>
    apiClient
      .post(`/executions/${id}/granularity_results`, data)
      .then((r) => r.data.data),

  updateLabels: (id: number, labels: Array<{ key: string; value: string }>) =>
    apiClient.patch(`/executions/${id}/labels`, { labels }).then((r) => r.data),

  batchDelete: (ids: number[]) =>
    apiClient.post('/executions/batch-delete', { ids }).then((r) => r.data),
};
