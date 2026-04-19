import type {
  ContainerDetailResp,
  ContainerResp,
  ContainerType,
  ContainerVersionResp,
  LabelItem,
  ListContainerResp,
  ListContainerVersionResp,
  StatusType,
} from '@rcabench/client';

import apiClient from './client';

export const containerApi = {
  getContainers: (params?: {
    page?: number;
    size?: number;
    type?: ContainerType;
    isPublic?: boolean;
    status?: StatusType;
  }): Promise<ListContainerResp> =>
    apiClient
      .get('/containers', { params })
      .then((r) => r.data.data ?? { items: [], total: 0 }),

  getContainer: (id: number): Promise<ContainerDetailResp> =>
    apiClient
      .get(`/containers/${id}`)
      .then((r) => r.data.data ?? ({} as ContainerDetailResp)),

  createContainer: (data: {
    name: string;
    type: ContainerType;
    readme?: string;
    is_public?: boolean;
  }): Promise<ContainerResp | undefined> =>
    apiClient.post('/containers', data).then((r) => r.data.data),

  updateContainer: (
    id: number,
    data: {
      name?: string;
      type?: ContainerType;
      readme?: string;
      is_public?: boolean;
      labels?: LabelItem[];
    }
  ) =>
    apiClient
      .patch<{ data: ContainerDetailResp }>(`/containers/${id}`, data)
      .then((r) => r.data.data),

  deleteContainer: (id: number) =>
    apiClient.delete(`/containers/${id}`).then((r) => r.data),

  updateLabels: (id: number, labels: Array<{ key: string; value: string }>) =>
    apiClient.patch(`/containers/${id}/labels`, { labels }).then((r) => r.data),

  buildContainer: (data: Record<string, unknown>) =>
    apiClient.post('/containers/build', data).then((r) => r.data.data),

  getVersions: (
    containerId: number,
    params?: { page?: number; size?: number; status?: StatusType }
  ): Promise<ListContainerVersionResp> =>
    apiClient
      .get(`/containers/${containerId}/versions`, { params })
      .then((r) => r.data.data ?? { items: [], total: 0 }),

  getVersion: (
    containerId: number,
    versionId: number
  ): Promise<ContainerVersionResp | undefined> =>
    apiClient
      .get(`/containers/${containerId}/versions/${versionId}`)
      .then((r) => r.data.data),

  createVersion: (
    containerId: number,
    data: {
      name: string;
      image_ref: string;
      command?: string;
    }
  ): Promise<ContainerVersionResp | undefined> =>
    apiClient
      .post(`/containers/${containerId}/versions`, data)
      .then((r) => r.data.data),

  updateVersion: (
    containerId: number,
    versionId: number,
    data: {
      name?: string;
      image_ref?: string;
      command?: string;
    }
  ) =>
    apiClient
      .patch<{
        data: ContainerVersionResp;
      }>(`/containers/${containerId}/versions/${versionId}`, data)
      .then((r) => r.data.data),

  deleteVersion: (containerId: number, versionId: number) =>
    apiClient
      .delete(`/containers/${containerId}/versions/${versionId}`)
      .then((r) => r.data),

  uploadHelmChart: (containerId: number, versionId: number, data: FormData) =>
    apiClient
      .post(
        `/containers/${containerId}/versions/${versionId}/helm-chart`,
        data,
        { headers: { 'Content-Type': 'multipart/form-data' } }
      )
      .then((r) => r.data),

  uploadHelmValues: (containerId: number, versionId: number, data: FormData) =>
    apiClient
      .post(
        `/containers/${containerId}/versions/${versionId}/helm-values`,
        data,
        { headers: { 'Content-Type': 'multipart/form-data' } }
      )
      .then((r) => r.data),
};
