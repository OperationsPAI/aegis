/**
 * Fetch projects with automatic name→id cache updates
 */
import type { ListProjectResp, StatusType } from '@rcabench/client';
import { useQuery, type UseQueryResult } from '@tanstack/react-query';

import { projectApi } from '@/api/projects';
import { updateProjectNameMap } from '@/utils/projectNameMap';

interface UseProjectsOptions {
  page?: number;
  size?: number;
  isPublic?: boolean;
  status?: StatusType;
  queryKey?: string | string[];
}

/**
 * Fetch projects and auto-update name→id cache
 */
export function useProjects(
  options: UseProjectsOptions = {}
): UseQueryResult<ListProjectResp | undefined> {
  const { page, size, isPublic, status, queryKey } = options;

  // Build query key based on provided options
  const baseKey = queryKey
    ? Array.isArray(queryKey)
      ? queryKey
      : [queryKey]
    : ['projects'];
  const finalQueryKey = [...baseKey, { page, size, isPublic, status }];

  return useQuery({
    queryKey: finalQueryKey,
    queryFn: async () => {
      const data = await projectApi.getProjects({
        page,
        size,
        isPublic,
        status,
      });

      // Update name→id cache
      if (data?.items) {
        updateProjectNameMap(data.items);
      }

      return data;
    },
  });
}
