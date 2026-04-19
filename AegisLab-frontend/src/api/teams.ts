import type {
  ListTeamResp,
  StatusType,
  TeamDetailResp,
  TeamMemberResp,
} from '@rcabench/client';

import type { ProjectResp } from '@/types/api';

import apiClient from './client';

export const teamApi = {
  getTeams: (params?: {
    page?: number;
    size?: number;
    isPublic?: boolean;
    status?: StatusType;
  }): Promise<ListTeamResp | undefined> =>
    apiClient.get('/teams', { params }).then((r) => r.data.data),

  getTeamDetail: (id: number): Promise<TeamDetailResp | undefined> =>
    apiClient.get(`/teams/${id}`).then((r) => r.data.data),

  createTeam: (data: {
    name: string;
    description?: string;
    is_public?: boolean;
  }) => apiClient.post('/teams', data).then((r) => r.data.data),

  updateTeam: (
    id: number,
    data: { name?: string; description?: string; is_public?: boolean }
  ) => apiClient.patch(`/teams/${id}`, data).then((r) => r.data.data),

  deleteTeam: (id: number) =>
    apiClient.delete(`/teams/${id}`).then((r) => r.data),

  getTeamMembers: async (
    teamId: number,
    params?: { page?: number; size?: number }
  ): Promise<{ items: TeamMemberResp[]; total: number }> => {
    const response = await apiClient.get(`/teams/${teamId}/members`, {
      params,
    });
    return {
      items: response.data.data?.items || [],
      total: response.data.data?.pagination?.total || 0,
    };
  },

  listTeamProjects: async (
    teamId: number,
    params?: { page?: number; size?: number }
  ): Promise<{ items: ProjectResp[]; total: number }> => {
    const response = await apiClient.get(`/teams/${teamId}/projects`, {
      params,
    });
    return {
      items: response.data.data?.items || [],
      total: response.data.data?.pagination?.total || 0,
    };
  },

  addMember: (teamId: number, data: { username: string; role_id: number }) =>
    apiClient.post(`/teams/${teamId}/members`, data).then((r) => r.data.data),

  removeMember: (teamId: number, userId: number) =>
    apiClient.delete(`/teams/${teamId}/members/${userId}`).then((r) => r.data),

  updateMemberRole: (teamId: number, userId: number, roleId: number) =>
    apiClient
      .patch(`/teams/${teamId}/members/${userId}/role`, { role_id: roleId })
      .then((r) => r.data),
};
