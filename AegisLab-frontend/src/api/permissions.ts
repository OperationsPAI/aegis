import type { PermissionDetailResp, PermissionResp } from '@rcabench/client';

import apiClient from './client';

export const permissionApi = {
  getPermissions: (params?: {
    page?: number;
    size?: number;
    action?: string;
    status?: string;
  }): Promise<PermissionResp[] | undefined> =>
    apiClient.get('/permissions', { params }).then((r) => r.data.data),

  getPermission: (id: number): Promise<PermissionDetailResp | undefined> =>
    apiClient.get(`/permissions/${id}`).then((r) => r.data.data),

  getPermissionRoles: (permissionId: number) =>
    apiClient
      .get(`/permissions/${permissionId}/roles`)
      .then((r) => r.data.data),
};
