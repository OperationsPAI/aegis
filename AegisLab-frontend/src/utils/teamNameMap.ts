/**
 * Team name→id mapping cache utility
 */
import type { Team } from '@/types/api';

export interface TeamNameMapEntry {
  id: number;
  name: string;
  cachedAt: number;
}

/**
 * Update name→id cache from team list
 */
export function updateTeamNameMap(
  teams: Team[] | undefined,
  setQueryData: (
    key: string[],
    data:
      | TeamNameMapEntry
      | ((old: TeamNameMapEntry | undefined) => TeamNameMapEntry)
  ) => void
): void {
  if (!teams || teams.length === 0) return;

  const now = Date.now();
  teams.forEach((team) => {
    if (team.id && team.name) {
      setQueryData(['teamNameMap', team.name], {
        id: team.id,
        name: team.name,
        cachedAt: now,
      });
    }
  });
}

/**
 * Get team ID from cache (5min TTL)
 */
export function getTeamIdFromName(
  teamName: string | undefined,
  getQueryData: (key: string[]) => TeamNameMapEntry | undefined
): number | undefined {
  if (!teamName) return undefined;

  const cached = getQueryData(['teamNameMap', teamName]);

  if (cached && Date.now() - cached.cachedAt < 5 * 60 * 1000) {
    return cached.id;
  }

  return undefined;
}
