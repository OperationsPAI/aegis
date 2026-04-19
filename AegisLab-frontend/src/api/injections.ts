import type {
  InjectionDetailResp,
  LabelItem,
  ListInjectionResp,
} from '@rcabench/client';

import apiClient, { fileClient } from './client';

// Re-export types for convenience
export type { InjectionDetailResp } from '@rcabench/client';

export const injectionApi = {
  listInjections: (params?: {
    page?: number;
    size?: number;
    fault_type?: string;
    benchmark?: string;
    state?: string;
    status?: number;
    labels?: string[];
  }): Promise<ListInjectionResp | undefined> =>
    apiClient.get('/injections', { params }).then((r) => r.data.data),

  searchInjections: (body?: {
    page?: number;
    size?: number;
    name_pattern?: string;
    sort?: Array<{ field: string; direction: string }>;
  }) => apiClient.post('/injections/search', body).then((r) => r.data.data),

  getInjection: (id: number): Promise<InjectionDetailResp | undefined> =>
    apiClient.get(`/injections/${id}`).then((r) => r.data.data),

  getMetadata: (params?: { system?: string }) =>
    apiClient.get('/injections/metadata', { params }).then((r) => r.data.data),

  cloneInjection: (id: number) =>
    apiClient.post(`/injections/${id}/clone`).then((r) => r.data.data),

  manageLabels: (
    id: number,
    add_labels: LabelItem[],
    remove_labels: LabelItem[]
  ) => {
    const removeLabelKeys = remove_labels
      .filter((l) => l.key !== undefined)
      .map((l) => l.key as string);
    return apiClient
      .patch(`/injections/${id}/labels`, {
        add_labels,
        remove_labels: removeLabelKeys,
      })
      .then((r) => r.data.data);
  },

  batchManageLabels: (data: {
    injection_ids: number[];
    add_labels?: LabelItem[];
    remove_labels?: string[];
  }) =>
    apiClient
      .patch('/injections/labels/batch', {
        items: data.injection_ids.map((id) => ({
          injection_id: id,
          add_labels: data.add_labels,
          remove_labels: data.remove_labels?.map((key) => ({ key })),
        })),
      })
      .then((r) => r.data.data),

  // NOTE: Use manageLabels() above for label management (add/remove pattern).
  // A whole-replace `updateLabels` was previously here but is unused and conflicts
  // with the add/remove endpoint the backend actually exposes.

  batchDelete: (ids: number[]) =>
    apiClient.post('/injections/batch-delete', { ids }).then((r) => r.data),

  listDatapackFiles: (id: number) =>
    apiClient.get(`/injections/${id}/files`).then((r) => r.data.data),

  downloadDatapackFile: async (id: number, path: string) => {
    const response = await fileClient.get(`/injections/${id}/files/download`, {
      params: { path },
    });
    return response.data;
  },

  downloadInjection: async (id: number) => {
    const response = await fileClient.get(`/injections/${id}/download`);
    return response.data;
  },

  uploadDatapack: async (params: {
    file: File;
    name: string;
    project_id: number;
    fault_type?: string;
    category?: string;
    benchmark_name?: string;
    pedestal_name?: string;
  }) => {
    const formData = new FormData();
    formData.append('file', params.file);
    formData.append('name', params.name);
    formData.append('project_id', String(params.project_id));
    if (params.fault_type) formData.append('fault_type', params.fault_type);
    if (params.category) formData.append('category', params.category);
    if (params.benchmark_name)
      formData.append('benchmark_name', params.benchmark_name);
    if (params.pedestal_name)
      formData.append('pedestal_name', params.pedestal_name);
    const response = await apiClient.post('/injections/upload', formData, {
      headers: { 'Content-Type': 'multipart/form-data' },
    });
    return response.data.data;
  },

  queryDatapackFileContent: async (
    id: number,
    path: string
  ): Promise<string> => {
    const response = await apiClient.get(`/injections/${id}/files/query`, {
      params: { path },
      // Return raw text so callers get the file content as a string
      transformResponse: [(data: string) => data],
    });
    return response.data;
  },
};
