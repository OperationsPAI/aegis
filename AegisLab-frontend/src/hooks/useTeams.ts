/**
 * Fetch teams with automatic name→id cache updates
 */
import type { ListTeamResp, TeamResp } from '@rcabench/client';
import {
  useQuery,
  useQueryClient,
  type UseQueryResult,
} from '@tanstack/react-query';

import { teamApi } from '@/api/teams';
import { updateTeamNameMap } from '@/utils/teamNameMap';

interface UseTeamsOptions {
  queryKey?: string | string[];
}

/**
 * Fetch teams and auto-update name→id cache
 */
export function useTeams(
  options: UseTeamsOptions = {}
): UseQueryResult<ListTeamResp | undefined> {
  const queryClient = useQueryClient();
  const { queryKey } = options;

  const baseKey = queryKey
    ? Array.isArray(queryKey)
      ? queryKey
      : [queryKey]
    : ['teams'];

  return useQuery({
    queryKey: baseKey,
    queryFn: async () => {
      const data = await teamApi.getTeams();

      // Update name→id cache
      if (data?.items) {
        const teams = data.items
          .filter(
            (t: TeamResp): t is TeamResp & { id: number; name: string } =>
              t.id != null && t.name != null
          )
          .map((t) => ({
            ...t,
            id: t.id,
            name: t.name,
            member_count: 0,
            project_count: 0,
            created_at: t.created_at ?? '',
            updated_at: t.updated_at ?? '',
          }));
        updateTeamNameMap(teams, (key, value) => {
          queryClient.setQueryData(key, value);
        });
      }

      return data;
    },
  });
}
