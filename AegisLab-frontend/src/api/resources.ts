import type { PermissionResp, ResourceResp } from '@rcabench/client';

import apiClient from './client';

export const resourceApi = {
  getResources: (params?: {
    page?: number;
    size?: number;
  }): Promise<{ items?: ResourceResp[]; total?: number } | undefined> =>
    apiClient.get('/resources', { params }).then((r) => r.data.data),

  getResource: (id: number): Promise<ResourceResp | undefined> =>
    apiClient.get(`/resources/${id}`).then((r) => r.data.data),

  getResourcePermissions: (id: number): Promise<PermissionResp[] | undefined> =>
    apiClient.get(`/resources/${id}/permissions`).then((r) => r.data.data),
};
