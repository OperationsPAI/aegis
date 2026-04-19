import type {
  CreateUserReq,
  ListUserResp,
  StatusType,
  UpdateUserReq,
  UserDetailResp,
  UserResp,
} from '@rcabench/client';

import apiClient from './client';

export const usersApi = {
  getUsers: (params?: {
    page?: number;
    size?: number;
    username?: string;
    email?: string;
    isActive?: boolean;
    status?: StatusType;
  }): Promise<ListUserResp | undefined> =>
    apiClient.get('/users', { params }).then((r) => r.data.data),

  getUserDetail: (id: number): Promise<UserDetailResp | undefined> =>
    apiClient.get(`/users/${id}/detail`).then((r) => r.data.data),

  createUser: (data: CreateUserReq): Promise<UserResp | undefined> =>
    apiClient.post('/users', data).then((r) => r.data.data),

  updateUser: (
    id: number,
    data: UpdateUserReq
  ): Promise<UserResp | undefined> =>
    apiClient.patch(`/users/${id}`, data).then((r) => r.data.data),

  deleteUser: (id: number): Promise<void> =>
    apiClient.delete(`/users/${id}`).then(() => undefined),

  // Role management
  assignRole: (userId: number, roleId: number) =>
    apiClient.post(`/users/${userId}/roles/${roleId}`).then((r) => r.data),

  removeRole: (userId: number, roleId: number) =>
    apiClient.delete(`/users/${userId}/roles/${roleId}`).then((r) => r.data),

  // Project role management
  assignProjectRole: (userId: number, projectId: number, roleId: number) =>
    apiClient
      .post(`/users/${userId}/projects/${projectId}/roles/${roleId}`)
      .then((r) => r.data),

  removeFromProject: (userId: number, projectId: number) =>
    apiClient
      .delete(`/users/${userId}/projects/${projectId}`)
      .then((r) => r.data),

  // Permission management
  assignPermissions: (userId: number, data: unknown) =>
    apiClient
      .post(`/users/${userId}/permissions/assign`, data)
      .then((r) => r.data),

  removePermissions: (userId: number, data: unknown) =>
    apiClient
      .post(`/users/${userId}/permissions/remove`, data)
      .then((r) => r.data),

  // Container role management
  assignContainerRole: (userId: number, containerId: number, roleId: number) =>
    apiClient
      .post(`/users/${userId}/containers/${containerId}/roles/${roleId}`)
      .then((r) => r.data),

  removeFromContainer: (userId: number, containerId: number) =>
    apiClient
      .delete(`/users/${userId}/containers/${containerId}`)
      .then((r) => r.data),

  // Dataset role management
  assignDatasetRole: (userId: number, datasetId: number, roleId: number) =>
    apiClient
      .post(`/users/${userId}/datasets/${datasetId}/roles/${roleId}`)
      .then((r) => r.data),

  removeFromDataset: (userId: number, datasetId: number) =>
    apiClient
      .delete(`/users/${userId}/datasets/${datasetId}`)
      .then((r) => r.data),
};

// Re-export types for use in other files
export type {
  UserResp,
  UserDetailResp,
  ListUserResp,
  CreateUserReq,
  UpdateUserReq,
};
