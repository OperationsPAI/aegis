import {
  type InjectionResp,
  type LabelItem,
  type ListExecutionResp,
  type ListProjectResp,
  type ProjectDetailResp,
  type ProjectResp,
  ProjectsApi,
  type SearchInjectionReq,
  type SortOption,
  type StatusType,
} from '@rcabench/client';

import apiClient from './client';
import { sdkAxios, sdkConfig } from './sdk';

const projectsSdk = new ProjectsApi(sdkConfig, '', sdkAxios);

export const projectApi = {
  getProjects: (params?: {
    page?: number;
    size?: number;
    isPublic?: boolean;
    status?: StatusType;
  }): Promise<ListProjectResp | undefined> =>
    projectsSdk
      .listProjects({
        page: params?.page,
        size: params?.size,
        isPublic: params?.isPublic,
        status: params?.status,
      })
      .then((r) => r.data.data),

  getProjectDetail: (id: number): Promise<ProjectDetailResp | undefined> =>
    projectsSdk.getProjectById({ projectId: id }).then((r) => r.data.data),

  createProject: (data: {
    name: string;
    description?: string;
    is_public?: boolean;
  }): Promise<ProjectResp | undefined> =>
    projectsSdk.createProject({ request: data }).then((r) => r.data.data),

  updateProject: (
    id: number,
    data: {
      name?: string;
      description?: string;
      is_public?: boolean;
      labels?: LabelItem[];
    }
  ) =>
    // SDK UpdateProjectReq doesn't include `name` or `labels`;
    // keep hand-rolled call for full backward compatibility
    apiClient
      .patch<{ data: ProjectDetailResp }>(`/projects/${id}`, data)
      .then((r) => r.data),

  deleteProject: (id: number) =>
    projectsSdk.deleteProject({ projectId: id }).then((r) => r.data),

  updateLabels: (id: number, labels: Array<{ key: string; value: string }>) =>
    apiClient.patch(`/projects/${id}/labels`, { labels }).then((r) => r.data),

  listProjectInjections: async (
    projectId: number,
    params?: { page?: number; size?: number }
  ): Promise<{ items: InjectionResp[]; total: number }> => {
    const response = await projectsSdk.listProjectInjections({
      projectId,
      page: params?.page,
      size: params?.size,
    });
    return {
      items: response.data.data?.items || [],
      total: response.data.data?.pagination?.total || 0,
    };
  },

  searchProjectInjections: async (
    projectId: number,
    body?: {
      page?: number;
      size?: number;
      search?: string;
      sort_by?: Array<{ field: string; order: 'asc' | 'desc' }>;
    }
  ): Promise<{ items: InjectionResp[]; total: number }> => {
    const search: SearchInjectionReq = {
      name_pattern: body?.search,
      page: body?.page,
      size: body?.size,
      sort: body?.sort_by?.map(
        (sf) =>
          ({
            field: sf.field,
            direction: sf.order,
          }) as SortOption
      ),
    };
    const response = await projectsSdk.searchProjectInjections({
      projectId,
      search,
    });
    return {
      items: (response.data.data?.items ?? []) as InjectionResp[],
      total: response.data.data?.pagination?.total ?? 0,
    };
  },

  // Keep hand-rolled: caller builds specs with a shape that doesn't match
  // the generated SubmitInjectionReq (SDK uses ChaosNode[][] vs runtime shape)
  submitInjection: (projectId: number, data: unknown) =>
    apiClient
      .post(`/projects/${projectId}/injections/inject`, data)
      .then((r) => r.data.data),

  buildDatapack: (projectId: number, data: unknown) =>
    apiClient
      .post(`/projects/${projectId}/injections/build`, data)
      .then((r) => r.data.data),

  getNoIssues: (
    projectId: number,
    params?: {
      labels?: string[];
      lookback?: string;
      customStartTime?: string;
      customEndTime?: string;
    }
  ) =>
    projectsSdk
      .listProjectInjectionsNoIssues({
        projectId,
        labels: params?.labels,
        lookback: params?.lookback,
        customStartTime: params?.customStartTime,
        customEndTime: params?.customEndTime,
      })
      .then((r) => r.data.data),

  getWithIssues: (
    projectId: number,
    params?: {
      labels?: string[];
      lookback?: string;
      customStartTime?: string;
      customEndTime?: string;
    }
  ) =>
    projectsSdk
      .listProjectInjectionsWithIssues({
        projectId,
        labels: params?.labels,
        lookback: params?.lookback,
        customStartTime: params?.customStartTime,
        customEndTime: params?.customEndTime,
      })
      .then((r) => r.data.data),

  getExecutions: (
    projectId: number,
    params?: { page?: number; size?: number }
  ): Promise<ListExecutionResp> =>
    projectsSdk
      .listProjectExecutions({
        projectId,
        page: params?.page,
        size: params?.size,
      })
      .then((r) => r.data.data as ListExecutionResp),

  executeAlgorithm: (projectId: number, data: unknown) =>
    apiClient
      .post(`/projects/${projectId}/executions/execute`, data)
      .then((r) => r.data.data),
};
