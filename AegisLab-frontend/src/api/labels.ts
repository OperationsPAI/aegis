import type { LabelDetailResp, LabelResp } from '@rcabench/client';

import apiClient from './client';

export const labelApi = {
  getLabels: (params?: { page?: number; size?: number; key?: string }) =>
    apiClient.get('/labels', { params }).then((r) => r.data.data),

  getLabel: (labelId: number): Promise<LabelDetailResp | undefined> =>
    apiClient.get(`/labels/${labelId}`).then((r) => r.data.data),

  createLabel: (data: {
    key: string;
    value: string;
    color?: string;
  }): Promise<LabelResp | undefined> =>
    apiClient.post('/labels', data).then((r) => r.data.data),

  updateLabel: (
    labelId: number,
    data: { key?: string; value?: string; color?: string }
  ): Promise<LabelResp | undefined> =>
    apiClient.patch(`/labels/${labelId}`, data).then((r) => r.data.data),

  deleteLabel: (labelId: number) =>
    apiClient.delete(`/labels/${labelId}`).then((r) => r.data),

  batchDeleteLabels: (data: { ids: number[] }) =>
    apiClient.post('/labels/batch-delete', data).then((r) => r.data),
};
