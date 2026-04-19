import type {
  DatasetDetailResp,
  DatasetVersionResp,
  LabelItem,
  ListDatasetResp,
  ListDatasetVersionResp,
  StatusType,
} from '@rcabench/client';

import apiClient, { fileClient } from './client';

type DatasetTypeParam = 'Trace' | 'Log' | 'Metric';

export const datasetApi = {
  getDatasets: (params?: {
    page?: number;
    size?: number;
    type?: DatasetTypeParam;
    is_public?: boolean;
    status?: StatusType;
  }): Promise<ListDatasetResp> =>
    apiClient
      .get('/datasets', { params })
      .then((r) => r.data.data ?? { items: [], total: 0 }),

  getDataset: (id: number): Promise<DatasetDetailResp> =>
    apiClient
      .get(`/datasets/${id}`)
      .then((r) => r.data.data ?? ({} as DatasetDetailResp)),

  createDataset: (data: {
    name: string;
    type: DatasetTypeParam;
    description?: string;
    is_public?: boolean;
  }) => apiClient.post('/datasets', data).then((r) => r.data.data),

  updateDataset: (
    id: number,
    data: {
      name?: string;
      type?: DatasetTypeParam;
      description?: string;
      is_public?: boolean;
      labels?: LabelItem[];
    }
  ) =>
    apiClient
      .patch<{ data: DatasetDetailResp }>(`/datasets/${id}`, data)
      .then((r) => r.data),

  deleteDataset: (id: number) =>
    apiClient.delete(`/datasets/${id}`).then((r) => r.data),

  updateLabels: (id: number, labels: Array<{ key: string; value: string }>) =>
    apiClient.patch(`/datasets/${id}/labels`, { labels }).then((r) => r.data),

  getVersions: (
    datasetId: number,
    params?: { page?: number; size?: number; status?: StatusType }
  ): Promise<ListDatasetVersionResp> =>
    apiClient
      .get(`/datasets/${datasetId}/versions`, { params })
      .then((r) => r.data.data ?? { items: [], total: 0 }),

  getVersion: (
    datasetId: number,
    versionId: number
  ): Promise<DatasetVersionResp | undefined> =>
    apiClient
      .get(`/datasets/${datasetId}/versions/${versionId}`)
      .then((r) => r.data.data),

  createVersion: (
    datasetId: number,
    data: { name: string; datapacks?: string[] }
  ): Promise<DatasetVersionResp | undefined> =>
    apiClient
      .post(`/datasets/${datasetId}/versions`, data)
      .then((r) => r.data.data),

  updateVersion: (
    datasetId: number,
    versionId: number,
    data: { name?: string; datapacks?: string[] }
  ) =>
    apiClient
      .patch<{
        data: DatasetVersionResp;
      }>(`/datasets/${datasetId}/versions/${versionId}`, data)
      .then((r) => r.data),

  deleteVersion: (datasetId: number, versionId: number) =>
    apiClient
      .delete(`/datasets/${datasetId}/versions/${versionId}`)
      .then((r) => r.data),

  updateVersionInjections: (
    datasetId: number,
    versionId: number,
    data: unknown
  ) =>
    apiClient
      .patch(`/datasets/${datasetId}/versions/${versionId}/injections`, data)
      .then((r) => r.data),

  downloadVersion: async (
    datasetId: number,
    versionId: number,
    fileName?: string
  ): Promise<void> => {
    const response = await fileClient.get(
      `/datasets/${datasetId}/versions/${versionId}/download`
    );
    const blob = new Blob([response.data as BlobPart]);
    const url = window.URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = fileName || `dataset-${datasetId}-v${versionId}.zip`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    window.URL.revokeObjectURL(url);
  },
};
