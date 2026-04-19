import type {
  CreateRoleReq,
  ListRoleResp,
  RoleDetailResp,
  RoleResp,
  UpdateRoleReq,
} from '@rcabench/client';

import apiClient from './client';

export const roleApi = {
  getRoles: (params?: {
    page?: number;
    size?: number;
    scope?: string;
  }): Promise<ListRoleResp | undefined> =>
    apiClient.get('/roles', { params }).then((r) => r.data.data),

  getRole: (id: number): Promise<RoleDetailResp | undefined> =>
    apiClient.get(`/roles/${id}`).then((r) => r.data.data),

  createRole: (data: CreateRoleReq): Promise<RoleResp | undefined> =>
    apiClient.post('/roles', data).then((r) => r.data.data),

  updateRole: (
    id: number,
    data: UpdateRoleReq
  ): Promise<RoleResp | undefined> =>
    apiClient.patch(`/roles/${id}`, data).then((r) => r.data.data),

  deleteRole: (id: number) =>
    apiClient.delete(`/roles/${id}`).then((r) => r.data),

  assignPermissions: (roleId: number, data: { permission_ids: number[] }) =>
    apiClient
      .post(`/roles/${roleId}/permissions/assign`, data)
      .then((r) => r.data),

  removePermissions: (roleId: number, data: { permission_ids: number[] }) =>
    apiClient
      .post(`/roles/${roleId}/permissions/remove`, data)
      .then((r) => r.data),

  getRoleUsers: (roleId: number) =>
    apiClient.get(`/roles/${roleId}/users`).then((r) => r.data.data),
};
